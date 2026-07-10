// Package vm decodes and executes DVD-Video virtual-machine commands.
//
// The instruction layout and execution behavior in this file are derived from
// VideoLAN libdvdnav 7.0.0 src/vm/decoder.c (commit
// 38238caf599dc9405eddf1531c858c725015f776), licensed GPL-2.0-or-later.
// See plans/dvd-menu-engine-proof-adr.md for provenance and scope.
package vm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

const (
	// GeneralRegisterCount is the number of DVD general parameter registers.
	GeneralRegisterCount = 16
	// SystemRegisterCount is the number of DVD system parameter registers.
	SystemRegisterCount = 24
	// DefaultCommandBudget bounds command evaluation when Options omits a limit.
	DefaultCommandBudget = 100_000
)

var (
	// ErrCommandBudget reports that evaluation exhausted its command budget.
	ErrCommandBudget = errors.New("DVD VM command budget exceeded")
	// ErrInvalidCommand reports a malformed instruction or invalid VM operand.
	ErrInvalidCommand = errors.New("invalid DVD VM command")
)

// Command is one authored eight-byte DVD VM instruction.
type Command [8]byte

// LinkCommand is a navigation transition produced by a VM instruction.
type LinkCommand uint8

// LinkCommand values mirror the authored DVD link, jump, call, exit, and
// resume operations consumed by graph traversal.
const (
	// LinkNoLink reports that an instruction emitted no navigation transition.
	LinkNoLink LinkCommand = iota
	LinkTopCell
	LinkNextCell
	LinkPreviousCell
	LinkReserved4
	LinkTopProgram
	LinkNextProgram
	LinkPreviousProgram
	LinkReserved8
	LinkTopPGC
	LinkNextPGC
	LinkPreviousPGC
	LinkGoUpPGC
	LinkTailPGC
	LinkReserved14
	LinkReserved15
	LinkResume
	LinkPGCN
	LinkPTTN
	LinkPGN
	LinkCN
	Exit
	JumpTT
	JumpVTSTT
	JumpVTSPTT
	JumpFirstPlay
	JumpVMGMMenu
	JumpVTSM
	JumpVMGMPGC
	CallFirstPlay
	CallVMGMMenu
	CallVTSM
	CallVMGMPGC
)

// Link describes the target and operands produced by a link/jump/call.
type Link struct {
	// Command identifies the emitted navigation operation.
	Command LinkCommand
	// Data1 is the command-specific first operand.
	Data1 uint16
	// Data2 is the command-specific second operand.
	Data2 uint16
	// Data3 is the command-specific third operand.
	Data3 uint16
}

// Registers is cloneable DVD VM register state.
type Registers struct {
	// General contains the 16 general parameter register values.
	General [GeneralRegisterCount]uint16
	// System contains the 24 system parameter register values.
	System [SystemRegisterCount]uint16
	// CounterMode marks general registers that advance from an internal origin time.
	CounterMode   [GeneralRegisterCount]bool
	counterOrigin [GeneralRegisterCount]time.Time
}

// Clone returns independent register state for traversal branching.
func (r Registers) Clone() Registers { return r }

// Options controls deterministic execution and resource limits.
type Options struct {
	// CommandBudget is the maximum number of evaluated rows; zero uses DefaultCommandBudget.
	CommandBudget int
	// Now supplies counter-register time; nil uses time.Now.
	Now func() time.Time
	// Random returns a value in [1,max]. It must be deterministic during graph
	// discovery. Nil selects 1, the lowest valid DVD RND result.
	Random func(upperBound uint16) uint16
}

// Result records whether command evaluation emitted a navigation transition.
type Result struct {
	// Link is the emitted transition when HasLink is true.
	Link Link
	// HasLink reports whether command evaluation emitted a transition.
	HasLink bool
	// Executed is the number of command rows evaluated.
	Executed int
}

type executor struct {
	registers *Registers
	now       func() time.Time
	random    func(uint16) uint16
}

// Execute evaluates an authored command table and mutates registers in place.
// It stops at the first navigation transition and rejects nil registers,
// invalid operands, and exhausted or negative command budgets.
func Execute(commands []Command, registers *Registers, opts Options) (Result, error) {
	if registers == nil {
		return Result{}, fmt.Errorf("%w: nil registers", ErrInvalidCommand)
	}
	budget := opts.CommandBudget
	if budget == 0 {
		budget = DefaultCommandBudget
	}
	if budget < 0 {
		return Result{}, fmt.Errorf("%w: negative command budget", ErrInvalidCommand)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	random := opts.Random
	if random == nil {
		random = func(upperBound uint16) uint16 {
			if upperBound == 0 {
				return 1
			}
			return 1
		}
	}

	e := executor{registers: registers, now: now, random: random}
	executed := 0
	for row := 0; row < len(commands); executed++ {
		if executed >= budget {
			return Result{Executed: executed}, ErrCommandBudget
		}
		line, link, hasLink, err := e.executeCommand(commands[row])
		if err != nil {
			return Result{Executed: executed + 1}, fmt.Errorf("DVD VM row %d: %w", row+1, err)
		}
		if hasLink {
			return Result{Link: link, HasLink: true, Executed: executed + 1}, nil
		}
		if line > 0 {
			if line == 256 {
				return Result{Executed: executed + 1}, nil
			}
			if line > len(commands) {
				return Result{Executed: executed + 1}, fmt.Errorf("%w: goto row %d outside table", ErrInvalidCommand, line)
			}
			row = line - 1
		} else {
			row++
		}
	}
	return Result{Executed: executed}, nil
}

func (e executor) executeCommand(command Command) (int, Link, bool, error) {
	instruction := binary.BigEndian.Uint64(command[:])
	typeCode := bits(instruction, 63, 3)
	var link Link

	switch typeCode {
	case 0:
		cond, err := e.condition1(instruction)
		if err != nil {
			return 0, link, false, err
		}
		if !cond {
			return 0, link, false, nil
		}
		switch bits(instruction, 51, 4) {
		case 0:
			return 0, link, false, nil
		case 1:
			return bitsInt(instruction, 7, 8), link, false, nil
		case 2:
			return 256, link, false, nil
		case 3:
			e.registers.System[13] = bits16(instruction, 11, 4)
			return bitsInt(instruction, 7, 8), link, false, nil
		default:
			return 0, link, false, fmt.Errorf("%w: special opcode %d", ErrInvalidCommand, bits(instruction, 51, 4))
		}
	case 1:
		if bits(instruction, 60, 1) != 0 {
			cond, err := e.condition2(instruction)
			if err != nil {
				return 0, link, false, err
			}
			link, ok := jumpInstruction(instruction)
			return 0, link, cond && ok, nil
		}
		cond, err := e.condition1(instruction)
		if err != nil {
			return 0, link, false, err
		}
		link, ok := linkInstruction(instruction)
		return 0, link, cond && ok, nil
	case 2:
		cond, err := e.condition2(instruction)
		if err != nil {
			return 0, link, false, err
		}
		if err := e.systemSet(instruction, cond); err != nil {
			return 0, link, false, err
		}
		if bits(instruction, 51, 4) != 0 {
			link, ok := linkInstruction(instruction)
			return 0, link, cond && ok, nil
		}
		return 0, link, false, nil
	case 3:
		cond, err := e.condition3(instruction)
		if err != nil {
			return 0, link, false, err
		}
		if err := e.setVersion1(instruction, cond); err != nil {
			return 0, link, false, err
		}
		if bits(instruction, 51, 4) != 0 {
			link, ok := linkInstruction(instruction)
			return 0, link, cond && ok, nil
		}
		return 0, link, false, nil
	case 4, 5, 6:
		cond := true
		var err error
		if typeCode != 4 {
			cond, err = e.condition4(instruction)
			if err != nil {
				return 0, link, false, err
			}
		}
		if err := e.setVersion2(instruction, cond); err != nil {
			return 0, link, false, err
		}
		if typeCode == 4 {
			cond, err = e.condition4(instruction)
			if err != nil {
				return 0, link, false, err
			}
		}
		link, ok := linkSubinstruction(instruction)
		if typeCode == 6 {
			cond = true
		}
		return 0, link, cond && ok, nil
	case 7:
		return 0, link, false, fmt.Errorf("%w: command type 7", ErrInvalidCommand)
	default:
		panic("unreachable")
	}
}

func bits(instruction uint64, start, count uint) uint64 {
	if count == 0 {
		return 0
	}
	shift := start + 1 - count
	mask := uint64(1)<<count - 1
	return instruction >> shift & mask
}

func bits16(instruction uint64, start, count uint) uint16 {
	if count > 16 {
		panic("DVD VM bits16 width exceeds 16")
	}
	//nolint:gosec // bits masks the value to at most 16 bits before conversion.
	return uint16(bits(instruction, start, count))
}

func bitsInt(instruction uint64, start, count uint) int {
	if count > 16 {
		panic("DVD VM bitsInt width exceeds bounded instruction fields")
	}
	//nolint:gosec // bits masks to at most 16 bits, which fits int on every target.
	return int(bits(instruction, start, count))
}

func (e executor) general(index uint64) (uint16, error) {
	if index >= GeneralRegisterCount {
		return 0, fmt.Errorf("%w: general register %d", ErrInvalidCommand, index)
	}
	if e.registers.CounterMode[index] {
		origin := e.registers.counterOrigin[index]
		if origin.IsZero() {
			origin = e.now()
		}
		seconds := e.now().Sub(origin) / time.Second
		//nolint:gosec // DVD counters intentionally retain the low 16 elapsed-second bits.
		value := uint16(uint64(seconds) & 0xffff)
		e.registers.General[index] = value
		return value, nil
	}
	return e.registers.General[index], nil
}

func (e executor) setGeneral(index uint64, value uint16) error {
	if index >= GeneralRegisterCount {
		return fmt.Errorf("%w: general register %d", ErrInvalidCommand, index)
	}
	if e.registers.CounterMode[index] {
		e.registers.counterOrigin[index] = e.now().Add(-time.Duration(value) * time.Second)
	}
	e.registers.General[index] = value
	return nil
}

func (e executor) register(code uint64) (uint16, error) {
	if code&0x80 != 0 {
		index := code & 0x1f
		if index >= SystemRegisterCount {
			return 0, fmt.Errorf("%w: system register %d", ErrInvalidCommand, index)
		}
		return e.registers.System[index], nil
	}
	return e.general(code & 0x0f)
}

func (e executor) registerOrData(instruction uint64, immediate, start uint) (uint16, error) {
	if bits(instruction, immediate, 1) != 0 {
		return bits16(instruction, start, 16), nil
	}
	return e.register(bits(instruction, start-8, 8))
}

func (e executor) generalOrData(instruction uint64, immediate, start uint) (uint16, error) {
	if bits(instruction, immediate, 1) != 0 {
		return bits16(instruction, start-1, 7), nil
	}
	return e.general(bits(instruction, start-4, 4))
}

func compare(op uint64, left, right uint16) (bool, error) {
	switch op {
	case 0:
		return true, nil
	case 1:
		return left&right != 0, nil
	case 2:
		return left == right, nil
	case 3:
		return left != right, nil
	case 4:
		return left >= right, nil
	case 5:
		return left > right, nil
	case 6:
		return left <= right, nil
	case 7:
		return left < right, nil
	default:
		return false, fmt.Errorf("%w: comparison opcode %d", ErrInvalidCommand, op)
	}
}

func (e executor) condition1(instruction uint64) (bool, error) {
	op := bits(instruction, 54, 3)
	if op == 0 {
		return true, nil
	}
	left, err := e.register(bits(instruction, 39, 8))
	if err != nil {
		return false, err
	}
	right, err := e.registerOrData(instruction, 55, 31)
	if err != nil {
		return false, err
	}
	return compare(op, left, right)
}

func (e executor) condition2(instruction uint64) (bool, error) {
	op := bits(instruction, 54, 3)
	if op == 0 {
		return true, nil
	}
	left, err := e.register(bits(instruction, 15, 8))
	if err != nil {
		return false, err
	}
	right, err := e.register(bits(instruction, 7, 8))
	if err != nil {
		return false, err
	}
	return compare(op, left, right)
}

func (e executor) condition3(instruction uint64) (bool, error) {
	op := bits(instruction, 54, 3)
	if op == 0 {
		return true, nil
	}
	left, err := e.register(bits(instruction, 47, 8))
	if err != nil {
		return false, err
	}
	right, err := e.registerOrData(instruction, 55, 15)
	if err != nil {
		return false, err
	}
	return compare(op, left, right)
}

func (e executor) condition4(instruction uint64) (bool, error) {
	op := bits(instruction, 54, 3)
	if op == 0 {
		return true, nil
	}
	left, err := e.register(bits(instruction, 51, 4))
	if err != nil {
		return false, err
	}
	right, err := e.registerOrData(instruction, 55, 31)
	if err != nil {
		return false, err
	}
	return compare(op, left, right)
}

func linkSubinstruction(instruction uint64) (Link, bool) {
	op := bits(instruction, 4, 5)
	if op > uint64(LinkResume) {
		return Link{}, false
	}
	return Link{Command: LinkCommand(op), Data1: bits16(instruction, 15, 6)}, op != 0
}

func linkInstruction(instruction uint64) (Link, bool) {
	switch bits(instruction, 51, 4) {
	case 1:
		return linkSubinstruction(instruction)
	case 4:
		return Link{Command: LinkPGCN, Data1: bits16(instruction, 14, 15)}, true
	case 5:
		return Link{Command: LinkPTTN, Data1: bits16(instruction, 9, 10), Data2: bits16(instruction, 15, 6)}, true
	case 6:
		return Link{Command: LinkPGN, Data1: bits16(instruction, 6, 7), Data2: bits16(instruction, 15, 6)}, true
	case 7:
		return Link{Command: LinkCN, Data1: bits16(instruction, 7, 8), Data2: bits16(instruction, 15, 6)}, true
	default:
		return Link{}, false
	}
}

func jumpInstruction(instruction uint64) (Link, bool) {
	switch bits(instruction, 51, 4) {
	case 1:
		return Link{Command: Exit}, true
	case 2:
		return Link{Command: JumpTT, Data1: bits16(instruction, 22, 7)}, true
	case 3:
		return Link{Command: JumpVTSTT, Data1: bits16(instruction, 22, 7)}, true
	case 5:
		return Link{Command: JumpVTSPTT, Data1: bits16(instruction, 22, 7), Data2: bits16(instruction, 41, 10)}, true
	case 6:
		switch bits(instruction, 23, 2) {
		case 0:
			return Link{Command: JumpFirstPlay}, true
		case 1:
			return Link{Command: JumpVMGMMenu, Data1: bits16(instruction, 19, 4)}, true
		case 2:
			return Link{Command: JumpVTSM, Data1: bits16(instruction, 31, 8), Data2: bits16(instruction, 39, 8), Data3: bits16(instruction, 19, 4)}, true
		case 3:
			return Link{Command: JumpVMGMPGC, Data1: bits16(instruction, 46, 15)}, true
		}
	case 8:
		resume := bits16(instruction, 31, 8)
		switch bits(instruction, 23, 2) {
		case 0:
			return Link{Command: CallFirstPlay, Data1: resume}, true
		case 1:
			return Link{Command: CallVMGMMenu, Data1: bits16(instruction, 19, 4), Data2: resume}, true
		case 2:
			return Link{Command: CallVTSM, Data1: bits16(instruction, 19, 4), Data2: resume}, true
		case 3:
			return Link{Command: CallVMGMPGC, Data1: bits16(instruction, 46, 15), Data2: resume}, true
		}
	}
	return Link{}, false
}

func (e executor) systemSet(instruction uint64, cond bool) error {
	switch bits(instruction, 59, 4) {
	case 1:
		for index := uint64(1); index <= 3; index++ {
			if bits(instruction, uint(63-(2+index)*8), 1) == 0 {
				continue
			}
			value, err := e.generalOrData(instruction, 60, uint(47-index*8))
			if err != nil {
				return err
			}
			if cond {
				e.registers.System[index] = value
			}
		}
	case 2:
		value, err := e.registerOrData(instruction, 60, 47)
		if err != nil {
			return err
		}
		if cond {
			e.registers.System[9] = value
			e.registers.System[10] = bits16(instruction, 23, 8)
		}
	case 3:
		value, err := e.registerOrData(instruction, 60, 47)
		if err != nil {
			return err
		}
		index := bits(instruction, 19, 4)
		e.registers.CounterMode[index] = bits(instruction, 23, 1) != 0
		if cond {
			return e.setGeneral(index, value)
		}
	case 6:
		value, err := e.registerOrData(instruction, 60, 31)
		if err != nil {
			return err
		}
		if cond {
			e.registers.System[8] = value
		}
	case 0:
		return nil
	default:
		return fmt.Errorf("%w: system-set opcode %d", ErrInvalidCommand, bits(instruction, 59, 4))
	}
	return nil
}

func (e executor) setVersion1(instruction uint64, cond bool) error {
	value, err := e.registerOrData(instruction, 60, 31)
	if err != nil || !cond {
		return err
	}
	return e.setOperation(bits(instruction, 59, 4), bits(instruction, 35, 4), bits(instruction, 19, 4), value)
}

func (e executor) setVersion2(instruction uint64, cond bool) error {
	value, err := e.registerOrData(instruction, 60, 47)
	if err != nil || !cond {
		return err
	}
	return e.setOperation(bits(instruction, 59, 4), bits(instruction, 51, 4), bits(instruction, 35, 4), value)
}

func (e executor) setOperation(op, register, register2 uint64, data uint16) error {
	current, err := e.general(register)
	if err != nil {
		return err
	}
	switch op {
	case 1:
		return e.setGeneral(register, data)
	case 2:
		if err := e.setGeneral(register2, current); err != nil {
			return err
		}
		return e.setGeneral(register, data)
	case 3:
		value := min(uint32(current)+uint32(data), 0xffff)
		return e.setGeneral(register, boundedUint16(value))
	case 4:
		if data > current {
			return e.setGeneral(register, 0)
		}
		return e.setGeneral(register, current-data)
	case 5:
		value := min(uint32(current)*uint32(data), 0xffff)
		return e.setGeneral(register, boundedUint16(value))
	case 6:
		if data == 0 {
			return e.setGeneral(register, 0xffff)
		}
		return e.setGeneral(register, current/data)
	case 7:
		if data == 0 {
			return e.setGeneral(register, 0xffff)
		}
		return e.setGeneral(register, current%data)
	case 8:
		value := e.random(data)
		if data == 0 {
			value = 1
		} else if value == 0 || value > data {
			return fmt.Errorf("%w: random result %d outside [1,%d]", ErrInvalidCommand, value, data)
		}
		return e.setGeneral(register, value)
	case 9:
		return e.setGeneral(register, current&data)
	case 10:
		return e.setGeneral(register, current|data)
	case 11:
		return e.setGeneral(register, current^data)
	case 0:
		return nil
	default:
		return fmt.Errorf("%w: set opcode %d", ErrInvalidCommand, op)
	}
}

func boundedUint16(value uint32) uint16 {
	if value > 0xffff {
		panic("DVD VM value outside uint16 range")
	}
	return uint16(value)
}
