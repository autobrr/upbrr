package vm

import (
	"errors"
	"testing"
)

func TestExecuteSetAndJump(t *testing.T) {
	set := makeCommand(
		field{63, 3, 3},
		field{60, 1, 1},
		field{59, 4, 1},
		field{35, 4, 2},
		field{31, 16, 42},
	)
	jump := makeCommand(
		field{63, 3, 1},
		field{60, 1, 1},
		field{51, 4, 2},
		field{22, 7, 7},
	)

	var registers Registers
	result, err := Execute([]Command{set, jump}, &registers, Options{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if registers.General[2] != 42 {
		t.Fatalf("GPRM[2] = %d, want 42", registers.General[2])
	}
	if !result.HasLink || result.Link.Command != JumpTT || result.Link.Data1 != 7 {
		t.Fatalf("result link = %+v, want JumpTT 7", result)
	}
}

func TestExecuteGotoBudget(t *testing.T) {
	loop := makeCommand(field{51, 4, 1}, field{7, 8, 1})
	var registers Registers
	result, err := Execute([]Command{loop}, &registers, Options{CommandBudget: 3})
	if !errors.Is(err, ErrCommandBudget) {
		t.Fatalf("Execute error = %v, want ErrCommandBudget", err)
	}
	if result.Executed != 3 {
		t.Fatalf("Executed = %d, want 3", result.Executed)
	}
}

func TestRegistersCloneIsIndependent(t *testing.T) {
	registers := Registers{}
	registers.General[1] = 10
	clone := registers.Clone()
	clone.General[1] = 20
	if registers.General[1] != 10 {
		t.Fatalf("parent GPRM mutated: %d", registers.General[1])
	}
}

func FuzzExecute(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{0x30, 0x01, 0, 0, 0, 0, 0, 1})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) != 8 {
			t.Skip()
		}
		var command Command
		copy(command[:], data)
		var registers Registers
		_, _ = Execute([]Command{command}, &registers, Options{CommandBudget: 2})
	})
}

type field struct {
	start uint
	count uint
	value uint64
}

func makeCommand(fields ...field) Command {
	var instruction uint64
	for _, item := range fields {
		shift := item.start + 1 - item.count
		mask := uint64(1)<<item.count - 1
		instruction |= item.value & mask << shift
	}
	var command Command
	for index := range command {
		command[index] = byte(instruction >> (56 - 8*index))
	}
	return command
}
