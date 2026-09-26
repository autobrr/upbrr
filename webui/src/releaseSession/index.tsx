// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ReactNode } from "react";
import { createContext, useContext, useEffect, useMemo, useReducer, useRef, useState } from "react";
import type {
  ApplicationInfo,
  OperationFailure,
  PrepareInput,
  ReleaseRef,
  ScreenshotPlan,
  ScreenshotPurpose,
  ScreenshotSelection,
  UploadImageHostFailure,
} from "../types";
import type {
  ActiveInputSnapshot,
  AudioAnalysisInstructions,
  DescriptionInstructions,
  ContinueReleaseWorkflowRequest,
  DupeDecision,
  MediaCaptureInstructions,
  Operation as WorkflowOperationStatus,
  RequiredAction,
  ReleaseWorkflowCurrent,
  MediaTrackFacts,
  WorkflowGoal,
  WorkflowIntent,
} from "../api/generated/release-workflow";
import type { ReleaseSessionPorts } from "./ports";
import { productionReleaseSessionPorts } from "./production";
import { initialSessionState, sessionReducer } from "./reducer";
import { canExecuteUpload } from "./uploadEligibility";
import { routeAccess, type TrackerWorkflowRequirements } from "./navigation";
import {
  cloneIntent,
  correctionPatchFor,
  emptyPreparationIntent,
  isActiveWorkflowOperation,
  isFailedWorkflowOperation,
  metadataPreviewFromWorkflow,
  normalizedNames,
  playlistSelectionComplete,
  preparationInputForWorkflow,
  preparationIntentFromInput,
  preparationWithEffectiveCorrections,
  preparationWithoutFactCorrections,
  sameNames,
  workflowDescriptionImageHostOverrides,
  workflowDescriptionScreenshotCount,
  workflowFactInstructions,
  workflowPreparationIntent,
  workflowPrepareInput,
  workflowSelectedInputTrackers,
  workflowViewValue,
  type PendingInputUpdate,
} from "./projections";
import type {
  AudioAnalysisGenerateInput,
  PreparationIntent,
  ReleaseRoute,
  ReleaseSession,
  RouteAccess,
  UploadRunOptions,
} from "./types";

const SessionContext = createContext<ReleaseSession | null>(null);

type WorkflowFacet = "screenshots" | "menuImages" | "uploadedImages" | "descriptions";
type ControllerKey = WorkflowFacet | "activeInput" | "preparation" | "workflow";
type WorkflowCommand = Readonly<{
  controller: AbortController;
  release: ReleaseRef;
  sessionRevision: number;
  revision: number;
}>;
type BackendCommandAuthority = Readonly<{
  commandID: string;
  operationID: string;
  operationSequence: number;
  workflowID: string;
  workflowRevision: number;
}>;
type WorkflowCommandFailureAuthority = Omit<BackendCommandAuthority, "commandID">;
type WorkflowCommandCallbacks = Readonly<{
  onAbort?: (authority: BackendCommandAuthority) => void;
  onError?: (error: unknown, authority: BackendCommandAuthority) => void;
  onStart?: (authority: BackendCommandAuthority) => void;
  onSuccess?: (current: ReleaseWorkflowCurrent, authority: BackendCommandAuthority) => void;
}>;
type ActiveSlotAuthority = Readonly<{
  revision: number;
  inputID: string;
  sourceVersion: string;
  workflowID: string;
}>;

const errorText = (error: unknown) =>
  error instanceof Error && error.message ? error.message : String(error);

const operationFailureFromError = (error: unknown): OperationFailure | null => {
  if (!error || typeof error !== "object" || !("failure" in error)) return null;
  const failure = (error as { failure?: unknown }).failure;
  if (!failure || typeof failure !== "object") return null;
  const candidate = failure as Partial<OperationFailure>;
  if (
    typeof candidate.Code !== "string" ||
    typeof candidate.Operation !== "string" ||
    typeof candidate.Message !== "string" ||
    typeof candidate.Recovery !== "string"
  ) {
    return null;
  }
  return candidate as OperationFailure;
};

const workflowOperationFailureError = (failure: Readonly<{ Message: string; Recovery: string }>) =>
  Object.assign(
    new Error(
      failure.Recovery && failure.Recovery !== "none"
        ? `${failure.Message} Recovery: ${failure.Recovery.replaceAll("_", " ")}.`
        : failure.Message,
    ),
    { failure },
  );

const workflowCommandFailureAuthority = Symbol("workflowCommandFailureAuthority");

const withWorkflowCommandFailureAuthority = (
  error: Error,
  current: ReleaseWorkflowCurrent,
  operation: WorkflowOperationStatus,
) =>
  Object.assign(error, {
    [workflowCommandFailureAuthority]: {
      operationID: operation.id,
      operationSequence: operation.sequence,
      workflowID: current.workflow.id,
      workflowRevision: current.workflow.revision,
    } satisfies WorkflowCommandFailureAuthority,
  });

const commandFailureAuthorityFromError = (
  error: unknown,
): WorkflowCommandFailureAuthority | null => {
  if (!error || typeof error !== "object" || !(workflowCommandFailureAuthority in error)) {
    return null;
  }
  return (error as { [workflowCommandFailureAuthority]: WorkflowCommandFailureAuthority })[
    workflowCommandFailureAuthority
  ];
};

const waitForWorkflowPoll = (signal: AbortSignal, delay = 1000) =>
  new Promise<void>((resolve, reject) => {
    if (signal.aborted) {
      reject(signal.reason ?? new DOMException("Aborted", "AbortError"));
      return;
    }
    const timeout = window.setTimeout(() => {
      signal.removeEventListener("abort", onAbort);
      resolve();
    }, delay);
    const onAbort = () => {
      window.clearTimeout(timeout);
      reject(signal.reason ?? new DOMException("Aborted", "AbortError"));
    };
    signal.addEventListener("abort", onAbort, { once: true });
  });

const workflowStorageKey = "upbrr.activeReleaseWorkflow";

const timestampedCommandID = (prefix: string, revision?: number) =>
  `${prefix}-${Date.now().toString(36)}${revision === undefined ? "" : `-${revision.toString(36)}`}`;

const storeWorkflowID = (workflowID: string) => {
  try {
    if (workflowID) window.sessionStorage.setItem(workflowStorageKey, workflowID);
    else window.sessionStorage.removeItem(workflowStorageKey);
  } catch {
    // Storage can be unavailable in hardened/private browser contexts.
  }
};

/** Owns canonical release workflow state, cancellation, correlation, and transport ports. */
export function ReleaseSessionProvider({
  ports,
  defaultTrackers = [],
  testRuntime,
  runtimeInfoReady = true,
  children,
}: Readonly<{
  ports?: ReleaseSessionPorts;
  defaultTrackers?: readonly string[];
  testRuntime?: ApplicationInfo["testRuntime"];
  runtimeInfoReady?: boolean;
  children: ReactNode;
}>) {
  const [state, dispatch] = useReducer(sessionReducer, undefined, initialSessionState);
  const [audioCommandFailure, setAudioCommandFailure] = useState<Readonly<{
    workflowID: string;
    workflowRevision: number;
    commandID: string;
    operationID: string;
    operationSequence: number;
    message: string;
  }> | null>(null);
  const [screenshotCommand, setScreenshotCommand] = useState<Readonly<{
    workflowID: string;
    workflowRevision: number;
    commandID: string;
    operationID: string;
    operationSequence: number;
    status: "running" | "error";
    message: string;
  }> | null>(null);
  const liveTest = testRuntime?.mode === "live_test";
  const mutationsAllowed = runtimeInfoReady && !liveTest;
  const uploadOptions = { ...state.uploadOptions, noSeed: liveTest || state.uploadOptions.noSeed };
  const normalizedDefaultTrackers = useMemo(
    () => normalizedNames(defaultTrackers),
    [defaultTrackers],
  );
  const controllers = useRef<Partial<Record<ControllerKey, AbortController>>>({});
  const stateRef = useRef(state);
  stateRef.current = state;
  const activeAuthority = useRef({
    state: "empty",
    revision: 0,
    inputID: "",
    sourceVersion: "",
    workflowID: "",
    workflowRevision: 0,
  });
  const preparationRevision = useRef(0);
  const lastPreparation = useRef<{
    operation: "prepare" | "reset";
    sourcePath: string;
    intent: PreparationIntent;
  } | null>(null);
  const workflowRevisions = useRef<Partial<Record<WorkflowFacet, number>>>({});
  const lastWorkflowError = useRef<unknown>(null);
  const activePorts = useMemo(() => ports ?? productionReleaseSessionPorts(), [ports]);
  const workflowView = state.workflowView;

  const backendCommandAuthority = (
    commandID: string,
    current: ReleaseWorkflowCurrent,
  ): BackendCommandAuthority => ({
    commandID,
    operationID: current.operation?.id || "",
    operationSequence: current.operation?.sequence || 0,
    workflowID: current.workflow.id,
    workflowRevision: current.workflow.revision,
  });

  const commandFailureSuperseded = (
    failure: BackendCommandAuthority,
    current: ReleaseWorkflowCurrent,
  ) => {
    if (failure.workflowID !== current.workflow.id) return true;
    if (current.workflow.revision > failure.workflowRevision) return true;
    if (!failure.operationID) return false;
    if (current.operation?.id !== failure.operationID) return true;
    return current.operation.sequence > failure.operationSequence;
  };

  const applyActiveInputSnapshot = (
    snapshot: ActiveInputSnapshot,
    status: "running" | "ready",
    capturedInputEditRevision: number,
    preserveInputDraft = false,
    submittedIntent?: PreparationIntent,
    requestedSourcePath?: string,
    submittedTrackers?: readonly string[],
    trackerInputsAccepted = false,
  ) => {
    const latest = activeAuthority.current;
    const inputID = snapshot.inputId || "";
    const sourceVersion = snapshot.sourceVersion || "";
    const workflowID = snapshot.current?.workflow.id || "";
    const workflowRevision = snapshot.current?.workflow.revision || 0;
    const maskedPreviousProcess =
      snapshot.state === "recovering" &&
      !snapshot.current &&
      !snapshot.inputId &&
      !snapshot.sourceVersion;
    if (snapshot.revision < latest.revision) return false;
    if (
      snapshot.revision === latest.revision &&
      latest.state === "recovering" &&
      !latest.workflowID &&
      workflowID &&
      !stateRef.current.activeInput.recoveryWorkflowIDs.includes(workflowID)
    ) {
      return false;
    }
    if (
      snapshot.revision === latest.revision &&
      !maskedPreviousProcess &&
      latest.inputID &&
      (inputID !== latest.inputID || sourceVersion !== latest.sourceVersion)
    ) {
      return false;
    }
    if (
      snapshot.revision === latest.revision &&
      !maskedPreviousProcess &&
      latest.workflowID &&
      (workflowID !== latest.workflowID || workflowRevision < latest.workflowRevision)
    ) {
      return false;
    }
    activeAuthority.current = {
      state: snapshot.state,
      revision: snapshot.revision,
      inputID,
      sourceVersion,
      workflowID,
      workflowRevision,
    };
    if (workflowID) storeWorkflowID(workflowID);
    else storeWorkflowID("");
    const current = snapshot.current || null;
    dispatch({
      type: "active_input_applied",
      snapshot,
      status,
      preview: current ? metadataPreviewFromWorkflow(current) : null,
      intent: current ? workflowPreparationIntent(current, submittedIntent) : null,
      capturedInputEditRevision,
      selectedTrackers: current
        ? (workflowSelectedInputTrackers(current) ?? submittedTrackers)
        : submittedTrackers,
      ...(requestedSourcePath ? { requestedSourcePath } : {}),
      trackerInputsAccepted,
      preserveInputDraft,
    });
    return true;
  };

  const activeSnapshotWithCurrent = (current: ReleaseWorkflowCurrent): ActiveInputSnapshot => ({
    state: activeAuthority.current.state,
    revision: activeAuthority.current.revision,
    ...(activeAuthority.current.inputID ? { inputId: activeAuthority.current.inputID } : {}),
    ...(activeAuthority.current.sourceVersion
      ? { sourceVersion: activeAuthority.current.sourceVersion }
      : {}),
    current,
  });

  const activeSlotAuthority = (): ActiveSlotAuthority => ({
    revision: activeAuthority.current.revision,
    inputID: activeAuthority.current.inputID,
    sourceVersion: activeAuthority.current.sourceVersion,
    workflowID: activeAuthority.current.workflowID,
  });

  const activeSlotMatches = (expected: ActiveSlotAuthority) => {
    const current = activeAuthority.current;
    return (
      current.revision === expected.revision &&
      current.inputID === expected.inputID &&
      current.sourceVersion === expected.sourceVersion &&
      current.workflowID === expected.workflowID
    );
  };

  const hasOpaqueRecoveringInput = () =>
    activeAuthority.current.state === "recovering" &&
    !activeAuthority.current.workflowID &&
    stateRef.current.activeInput.recoveryWorkflowIDs.length === 0;

  useEffect(() => {
    dispatch({
      type: "default_trackers_received",
      sessionRevision: state.sessionRevision,
      trackers: normalizedDefaultTrackers,
    });
  }, [normalizedDefaultTrackers, state.sessionRevision]);

  const publishWorkflowCurrent = (current: ReleaseWorkflowCurrent, status: "running" | "ready") => {
    const authority = activeAuthority.current;
    if (
      authority.workflowID !== current.workflow.id ||
      current.workflow.revision < authority.workflowRevision
    ) {
      return current;
    }
    activeAuthority.current = {
      ...authority,
      workflowRevision: current.workflow.revision,
    };
    storeWorkflowID(current.workflow.id);
    const selectedTrackers = workflowSelectedInputTrackers(current);
    if (selectedTrackers) {
      dispatch({ type: "trackers_received", trackers: selectedTrackers });
    }
    setAudioCommandFailure((failure) =>
      failure && commandFailureSuperseded(failure, current) ? null : failure,
    );
    setScreenshotCommand((command) =>
      command?.status === "error" && commandFailureSuperseded(command, current) ? null : command,
    );
    dispatch({ type: "workflow_current_published", status, current });
    return current;
  };

  const acceptWorkflowCurrent = (current: ReleaseWorkflowCurrent) =>
    publishWorkflowCurrent(current, "ready");

  const releaseWorkflowController = (controller: AbortController) => {
    if (controllers.current.workflow === controller) delete controllers.current.workflow;
  };

  const awaitWorkflowCommand = async (
    initial: ReleaseWorkflowCurrent,
    signal: AbortSignal,
    expectedSlot = activeSlotAuthority(),
  ): Promise<ReleaseWorkflowCurrent> => {
    const workflowID = initial.workflow.id;
    if (
      signal.aborted ||
      !activeSlotMatches(expectedSlot) ||
      expectedSlot.workflowID !== workflowID
    ) {
      throw new DOMException("Active input changed.", "AbortError");
    }
    publishWorkflowCurrent(initial, "running");
    let operation = initial.operation;
    if (!isActiveWorkflowOperation(operation)) return initial;

    while (operation && isActiveWorkflowOperation(operation)) {
      await waitForWorkflowPoll(signal);
      if (signal.aborted || !activeSlotMatches(expectedSlot)) {
        throw new DOMException("Active input changed.", "AbortError");
      }
      operation = await activePorts.workflow.operation(workflowID, operation.id, signal);
      if (signal.aborted || !activeSlotMatches(expectedSlot)) {
        throw new DOMException("Active input changed.", "AbortError");
      }
      const update = operation;
      dispatch({
        type: "workflow_operation_updated",
        workflowID: initial.workflow.id,
        operation: update,
      });
    }

    if (signal.aborted || !activeSlotMatches(expectedSlot)) {
      throw new DOMException("Active input changed.", "AbortError");
    }
    const current = await activePorts.workflow.current(workflowID, signal);
    if (signal.aborted || !activeSlotMatches(expectedSlot)) {
      throw new DOMException("Active input changed.", "AbortError");
    }
    publishWorkflowCurrent(current, "running");
    const terminalOperation = operation as WorkflowOperationStatus | undefined;
    if (terminalOperation && isFailedWorkflowOperation(terminalOperation)) {
      const failure = terminalOperation.failures?.[0]?.failure;
      if (failure) {
        throw withWorkflowCommandFailureAuthority(
          workflowOperationFailureError(failure),
          current,
          terminalOperation,
        );
      }
      throw withWorkflowCommandFailureAuthority(
        new Error(terminalOperation.message || `Workflow operation ${terminalOperation.status}.`),
        current,
        terminalOperation,
      );
    }
    return current;
  };

  const awaitWorkflowOperationTerminal = async (
    workflowID: string,
    initial: WorkflowOperationStatus,
    signal: AbortSignal,
  ): Promise<WorkflowOperationStatus> => {
    let operation = initial;
    while (isActiveWorkflowOperation(operation)) {
      await waitForWorkflowPoll(signal);
      operation = await activePorts.workflow.operation(workflowID, operation.id, signal);
      dispatch({ type: "workflow_operation_updated", workflowID, operation });
    }
    return operation;
  };

  const failBackendWorkflow = (error: unknown) => {
    const failure = operationFailureFromError(error);
    if (failure?.Code === "missing_prerequisite" && failure.Recovery === "refresh_release") {
      storeWorkflowID("");
    }
    dispatch({
      type: "workflow_view_failed",
      error: failure?.Message || "Workflow request failed. Retry the request.",
      failure,
    });
    return null;
  };

  const continueBackendGoal = async (
    initial: ReleaseWorkflowCurrent,
    goal: WorkflowGoal,
    intent: WorkflowIntent,
    idempotencyKey: string,
    signal: AbortSignal,
    extra: Pick<ContinueReleaseWorkflowRequest, "answers" | "approval"> = {},
  ): Promise<ReleaseWorkflowCurrent> => {
    if (goal === "uploaded" && !mutationsAllowed) {
      throw new Error("Tracker submission is unavailable in this runtime.");
    }
    let current = initial;
    let nextIntent = intent;
    for (let transition = 0; transition < 32; transition += 1) {
      if (activeAuthority.current.workflowID !== current.workflow.id) {
        throw new DOMException("Active input changed.", "AbortError");
      }
      const expectedSlot = activeSlotAuthority();
      const continued = await activePorts.workflow.continue(
        {
          authority: {
            workflowId: current.workflow.id,
            expectedRevision: current.workflow.revision,
          },
          goal,
          intent: { interaction: "interactive", ...nextIntent },
          idempotencyKey,
          ...extra,
        },
        signal,
      );
      if (signal.aborted || !activeSlotMatches(expectedSlot)) {
        throw new DOMException("Active input changed.", "AbortError");
      }
      const next = await awaitWorkflowCommand(continued, signal, expectedSlot);
      if (next.workflow.revision === current.workflow.revision) return next;
      current = next;
      const { correctionPatch: _acceptedPatch, ...remainingIntent } = nextIntent;
      nextIntent = remainingIntent;
      if (nextIntent.preparation && current.factInstructions) {
        nextIntent = {
          ...nextIntent,
          preparation: preparationWithEffectiveCorrections(
            nextIntent.preparation,
            current.factInstructions.instructions,
          ),
        };
      }
      if (
        current.release &&
        current.factInstructions &&
        current.release.factInstructions.id === current.factInstructions.id &&
        current.release.factInstructions.revision === current.factInstructions.revision
      ) {
        const {
          factInstructions: _acceptedFacts,
          preparation: _acceptedPreparation,
          ...goalIntent
        } = nextIntent;
        nextIntent = goalIntent;
      }
    }
    throw new Error("Release workflow continuation exceeded the transition limit.");
  };

  const dispatchPlaylistAction = (
    current: ReleaseWorkflowCurrent,
    sourcePath: string,
    commandRevision: number,
    correlationID: string,
  ) => {
    const action = current.workflow.requiredActions?.find(
      (candidate) => candidate.kind === "select_playlist" && candidate.status === "pending",
    );
    if (!action) return false;
    dispatch({
      type: "playlist_required",
      sourcePath,
      commandRevision,
      correlationID,
      candidates: (action.options || []).map((option) =>
        option.playlist
          ? { ...option.playlist, items: [...option.playlist.items] }
          : {
              id: option.value,
              discId: "",
              discName: "",
              file: option.label || option.value,
              duration: 0,
              items: [],
              score: 0,
              edition: "",
            },
      ),
      error: "",
    });
    return true;
  };

  const reloadBackendWorkflow = async (
    preserveInputDraft = false,
    completedController?: AbortController,
  ): Promise<boolean> => {
    if (controllers.current.activeInput) return false;
    const controller = new AbortController();
    controllers.current.activeInput = controller;
    const capturedInputEditRevision = stateRef.current.inputEditRevision;
    const preserveCurrentDraft = preserveInputDraft || stateRef.current.preparationDirty;
    const draftInputID = stateRef.current.activeInput.inputID;
    const draftSourceVersion = stateRef.current.activeInput.sourceVersion;
    const keepLocalDraft = (snapshot: ActiveInputSnapshot) =>
      preserveCurrentDraft &&
      (snapshot.inputId || "") === draftInputID &&
      (snapshot.sourceVersion || "") === draftSourceVersion;
    const completionStatus = () => {
      const workflowController = controllers.current.workflow;
      const preparationController = controllers.current.preparation;
      return (workflowController && workflowController !== completedController) ||
        (preparationController && preparationController !== completedController)
        ? "running"
        : "ready";
    };
    try {
      const snapshot = await activePorts.activeInput.get(controller.signal);
      if (controller.signal.aborted) return false;
      if (!snapshot.current) {
        return applyActiveInputSnapshot(
          snapshot,
          completionStatus(),
          capturedInputEditRevision,
          keepLocalDraft(snapshot),
        );
      }
      if (
        !applyActiveInputSnapshot(
          snapshot,
          "running",
          capturedInputEditRevision,
          keepLocalDraft(snapshot),
        )
      ) {
        return false;
      }
      const current = await awaitWorkflowCommand(snapshot.current, controller.signal);
      if (controller.signal.aborted) return false;
      const accepted = applyActiveInputSnapshot(
        { ...snapshot, current },
        completionStatus(),
        capturedInputEditRevision,
        keepLocalDraft(snapshot),
      );
      const sourcePath = current.release?.release.Source.SourcePath || "";
      if (accepted && sourcePath) {
        preparationRevision.current = Math.max(
          preparationRevision.current,
          current.workflow.revision,
        );
        lastPreparation.current = {
          operation: "prepare",
          sourcePath,
          intent: workflowPreparationIntent(current),
        };
      }
      return true;
    } catch (error) {
      if (!controller.signal.aborted) {
        dispatch({
          type: "workflow_view_failed",
          error: errorText(error),
          failure: operationFailureFromError(error),
        });
      }
      return false;
    } finally {
      if (controllers.current.activeInput === controller) delete controllers.current.activeInput;
    }
  };

  const startBackendWorkflow = async (
    input: PrepareInput,
    update: PendingInputUpdate = {
      inputEditRevision: state.inputEditRevision,
      correctionDirty: state.correctionDirty,
      resetFields: state.correctionResetFields,
      confirmFields: state.correctionConfirmFields,
      valueFields: state.correctionValueFields,
      trackerInputAnswers: state.trackerInputAnswers,
      selectedTrackers: state.selectedTrackers,
    },
    attempt?: Readonly<{ correlationID: string; controller: AbortController }>,
  ): Promise<ReleaseWorkflowCurrent | null> => {
    if (activeAuthority.current.state === "recovering" && !hasOpaqueRecoveringInput()) return null;
    if (controllers.current.workflow) return null;
    abortController("activeInput");
    const controller = attempt?.controller ?? new AbortController();
    controllers.current.workflow = controller;
    dispatch({ type: "active_input_loading" });
    const commandID =
      attempt?.correlationID || timestampedCommandID("workflow", state.commandRevision);
    const expectedRevision = activeAuthority.current.revision;
    lastWorkflowError.current = null;
    try {
      const previous = workflowView.current;
      const correctionPatch =
        update.correctionDirty && previous
          ? correctionPatchFor(previous, preparationIntentFromInput(input), update)
          : undefined;
      const preparation = workflowPrepareInput(input);
      const intent: WorkflowIntent = {
        ...(correctionPatch ? { correctionPatch } : {}),
        ...(correctionPatch
          ? {}
          : { factInstructions: workflowFactInstructions(input.Instructions) }),
        preparation: correctionPatch ? preparationWithoutFactCorrections(preparation) : preparation,
        trackerIds: [...update.selectedTrackers],
      };
      const snapshot = await activePorts.activeInput.open(
        {
          expectedRevision,
          request: {
            goal: "input_ready",
            intent,
            idempotencyKey: commandID,
          },
        },
        controller.signal,
      );
      if (
        controller.signal.aborted ||
        !applyActiveInputSnapshot(
          snapshot,
          "running",
          update.inputEditRevision,
          false,
          preparationIntentFromInput(input),
          input.SourcePath,
          update.selectedTrackers,
        ) ||
        !snapshot.current
      ) {
        return null;
      }
      const created = await awaitWorkflowCommand(snapshot.current, controller.signal);
      let continuationIntent: WorkflowIntent = { ...intent, correctionPatch: undefined };
      if (correctionPatch && created.factInstructions && continuationIntent.preparation) {
        continuationIntent = {
          ...continuationIntent,
          preparation: preparationWithEffectiveCorrections(
            continuationIntent.preparation,
            created.factInstructions.instructions,
          ),
        };
      }
      const prepared = await continueBackendGoal(
        created,
        "input_ready",
        continuationIntent,
        commandID,
        controller.signal,
      );
      releaseWorkflowController(controller);
      if (controller.signal.aborted) return null;
      return prepared;
    } catch (error) {
      releaseWorkflowController(controller);
      lastWorkflowError.current = error;
      if (!controller.signal.aborted) {
        failBackendWorkflow(error);
        if (operationFailureFromError(error)?.Recovery === "review_again") {
          await reloadBackendWorkflow(true, controller);
        }
      }
      return null;
    } finally {
      releaseWorkflowController(controller);
    }
  };

  const runBackendWorkflow = async (
    execute: (
      current: ReleaseWorkflowCurrent,
      commandID: string,
      signal: AbortSignal,
    ) => Promise<ReleaseWorkflowCurrent>,
    callbacks: WorkflowCommandCallbacks = {},
  ): Promise<boolean> => {
    if (activeAuthority.current.state === "recovering") return false;
    if (!workflowView.current || controllers.current.workflow) return false;
    const controller = new AbortController();
    controllers.current.workflow = controller;
    dispatch({ type: "active_input_loading" });
    const commandID = `workflow-${Date.now().toString(36)}-${workflowView.current.workflow.revision.toString(36)}`;
    let commandAuthority = {
      ...backendCommandAuthority(commandID, workflowView.current),
      operationID: "",
      operationSequence: 0,
    };
    callbacks.onStart?.(commandAuthority);
    try {
      const initial = await execute(workflowView.current, commandID, controller.signal);
      commandAuthority = backendCommandAuthority(commandID, initial);
      const current = await awaitWorkflowCommand(initial, controller.signal);
      releaseWorkflowController(controller);
      if (controller.signal.aborted) return false;
      acceptWorkflowCurrent(current);
      callbacks.onSuccess?.(current, commandAuthority);
      return true;
    } catch (error) {
      releaseWorkflowController(controller);
      if (controller.signal.aborted) {
        callbacks.onAbort?.(commandAuthority);
      } else {
        const failureAuthority = commandFailureAuthorityFromError(error);
        if (failureAuthority?.workflowID === commandAuthority.workflowID) {
          commandAuthority = {
            commandID,
            ...failureAuthority,
          };
        }
        callbacks.onError?.(error, commandAuthority);
        failBackendWorkflow(error);
      }
      return false;
    } finally {
      releaseWorkflowController(controller);
    }
  };

  const reconcileRecoveryAction = async (action: RequiredAction): Promise<boolean> => {
    const current = workflowView.current;
    if (
      (activeAuthority.current.state !== "recovering" &&
        activeAuthority.current.state !== "active") ||
      !runtimeInfoReady ||
      !current ||
      controllers.current.activeInput ||
      action.kind !== "reconcile_submission" ||
      !action.options?.some((option) => option.value === "not_completed")
    ) {
      return false;
    }
    const retainedAction = current.workflow.requiredActions?.find(
      (candidate) =>
        candidate.id === action.id &&
        candidate.kind === "reconcile_submission" &&
        candidate.status === "pending",
    );
    if (!retainedAction) return false;

    const controller = new AbortController();
    controllers.current.activeInput = controller;
    dispatch({ type: "active_input_loading" });
    const commandID = timestampedCommandID("workflow-reconcile", current.workflow.revision);
    try {
      const snapshot = await activePorts.activeInput.reconcile(
        {
          authority: {
            workflowId: current.workflow.id,
            expectedRevision: current.workflow.revision,
          },
          answer: {
            actionId: retainedAction.id,
            workflowRevision: current.workflow.revision,
            selectedValues: ["not_completed"],
          },
          idempotencyKey: commandID,
        },
        controller.signal,
      );
      if (controller.signal.aborted) return false;
      return applyActiveInputSnapshot(snapshot, "ready", stateRef.current.inputEditRevision);
    } catch (error) {
      if (!controller.signal.aborted) {
        dispatch({
          type: "workflow_view_failed",
          error: errorText(error),
          failure: operationFailureFromError(error),
        });
      }
      return false;
    } finally {
      if (controllers.current.activeInput === controller) delete controllers.current.activeInput;
    }
  };

  const cancelBackendWorkflow = async (reason: string): Promise<boolean> => {
    const workflowID = workflowView.current?.workflow.id || activeAuthority.current.workflowID;
    if (!workflowID) return false;
    const operation = workflowView.current?.operation;
    abortController("workflow");
    const controller = new AbortController();
    controllers.current.workflow = controller;
    dispatch({ type: "active_input_loading" });
    const commandID = timestampedCommandID("workflow-cancel");
    try {
      let current: ReleaseWorkflowCurrent;
      const cancelingOperation = isActiveWorkflowOperation(operation);
      if (cancelingOperation) {
        await activePorts.workflow.cancelOperation(workflowID, operation.id, controller.signal);
        current = await activePorts.workflow.current(workflowID, controller.signal);
      } else {
        current = await activePorts.workflow.cancel(
          workflowID,
          reason,
          commandID,
          controller.signal,
        );
      }
      releaseWorkflowController(controller);
      if (controller.signal.aborted) return false;
      acceptWorkflowCurrent(current);
      if (!cancelingOperation) storeWorkflowID("");
      return true;
    } catch (error) {
      releaseWorkflowController(controller);
      if (!controller.signal.aborted) failBackendWorkflow(error);
      return false;
    } finally {
      releaseWorkflowController(controller);
    }
  };

  const checkBackendDuplicates = async (): Promise<boolean> => {
    if (
      !workflowView.current ||
      controllers.current.workflow ||
      state.selectedTrackers.length === 0
    ) {
      return false;
    }
    const controller = new AbortController();
    controllers.current.workflow = controller;
    dispatch({ type: "active_input_loading" });
    const commandID = `workflow-dupes-${Date.now().toString(36)}-${workflowView.current.workflow.revision.toString(36)}`;
    try {
      const currentProjectionByTracker = new Map(
        (workflowView.current.projections?.projections || []).map((projection) => [
          projection.trackerId,
          projection,
        ]),
      );
      const projectionInstructions = Object.fromEntries(
        state.selectedTrackers.map((tracker) => {
          const projection = currentProjectionByTracker.get(tracker);
          const confirmationRequired = projection?.policyDecisions?.some(
            (decision) =>
              decision.code === "release_name_confirmation" &&
              decision.decision === "confirmation_required",
          );
          const retainedName =
            workflowView.current?.projectionInstructions?.instructions[tracker]?.uploadReleaseName;
          const confirmedName = confirmationRequired
            ? undefined
            : (state.releaseNameOverrides[tracker] ?? retainedName ?? undefined);
          return [
            tracker,
            {
              questionnaire: Object.fromEntries(
                Object.entries(state.questionnaireAnswers[tracker] || {}).map(([key, value]) => [
                  key,
                  value,
                ]),
              ),
              ...(confirmedName !== undefined ? { uploadReleaseName: confirmedName } : {}),
            },
          ];
        }),
      );
      const current = await continueBackendGoal(
        workflowView.current,
        "duplicates_decided",
        {
          trackerIds: [...state.selectedTrackers],
          projectionInstructions,
          skipRemoteDuplicates: false,
        },
        commandID,
        controller.signal,
      );
      releaseWorkflowController(controller);
      if (controller.signal.aborted) return false;
      acceptWorkflowCurrent(current);
      return true;
    } catch (error) {
      releaseWorkflowController(controller);
      if (!controller.signal.aborted) failBackendWorkflow(error);
      return false;
    } finally {
      releaseWorkflowController(controller);
    }
  };

  const reloadActiveInputRef = useRef(reloadBackendWorkflow);
  reloadActiveInputRef.current = reloadBackendWorkflow;

  useEffect(() => {
    const reloadVisible = () => {
      if (document.visibilityState !== "hidden") void reloadActiveInputRef.current();
    };
    const unsubscribe = activePorts.activeInput.subscribe(reloadVisible, (update) => {
      dispatch({ type: "source_verification_progressed", update });
    });
    const interval = window.setInterval(reloadVisible, 15_000);
    window.addEventListener("focus", reloadVisible);
    window.addEventListener("online", reloadVisible);
    document.addEventListener("visibilitychange", reloadVisible);
    void reloadActiveInputRef.current();
    return () => {
      unsubscribe();
      window.clearInterval(interval);
      window.removeEventListener("focus", reloadVisible);
      window.removeEventListener("online", reloadVisible);
      document.removeEventListener("visibilitychange", reloadVisible);
    };
  }, [activePorts.activeInput]);

  const abortController = (key: ControllerKey) => {
    controllers.current[key]?.abort();
    delete controllers.current[key];
  };

  const abortAll = () => {
    Object.values(controllers.current).forEach((controller) => controller?.abort());
    controllers.current = {};
  };

  const abortWorkflowFacets = () => {
    (["screenshots", "menuImages", "uploadedImages", "descriptions"] as const).forEach((facet) => {
      if (!controllers.current[facet]) return;
      controllers.current[facet]?.abort();
      delete controllers.current[facet];
      dispatch({
        type: "workflow_canceled",
        facet,
        sessionRevision: stateRef.current.sessionRevision,
        revision: stateRef.current[facet].revision,
        reason: "Preparation required.",
      });
    });
  };

  useEffect(() => abortAll, []);

  const cancelPreparation = () => {
    const preparation = stateRef.current.preparation;
    if (preparation.status !== "running") return;
    abortController("preparation");
    abortController("workflow");
    dispatch({ type: "preparation_cancelled", correlationID: preparation.correlationID });
  };

  const releaseActiveInput = async (): Promise<boolean> => {
    if (
      activeAuthority.current.state === "empty" ||
      activeAuthority.current.state === "recovering"
    ) {
      return false;
    }
    abortController("activeInput");
    abortController("preparation");
    abortController("workflow");
    abortWorkflowFacets();
    const controller = new AbortController();
    controllers.current.activeInput = controller;
    const expectedRevision = activeAuthority.current.revision;
    dispatch({ type: "active_input_loading" });
    try {
      const snapshot = await activePorts.activeInput.release(
        { expectedRevision },
        controller.signal,
      );
      if (controller.signal.aborted) return false;
      return applyActiveInputSnapshot(snapshot, "ready", stateRef.current.inputEditRevision);
    } catch (error) {
      if (!controller.signal.aborted) {
        dispatch({
          type: "workflow_view_failed",
          error: errorText(error),
          failure: operationFailureFromError(error),
        });
        if (operationFailureFromError(error)?.Recovery === "review_again") {
          if (controllers.current.activeInput === controller)
            delete controllers.current.activeInput;
          await reloadBackendWorkflow();
        }
      }
      return false;
    } finally {
      if (controllers.current.activeInput === controller) delete controllers.current.activeInput;
    }
  };

  const recoverLegacyWorkflow = async (workflowID: string): Promise<boolean> => {
    const normalizedWorkflowID = workflowID.trim();
    if (
      (activeAuthority.current.state !== "empty" &&
        activeAuthority.current.state !== "recovering") ||
      !normalizedWorkflowID ||
      !stateRef.current.activeInput.recoveryWorkflowIDs.includes(normalizedWorkflowID) ||
      controllers.current.activeInput
    ) {
      return false;
    }
    const controller = new AbortController();
    controllers.current.activeInput = controller;
    dispatch({ type: "active_input_loading" });
    try {
      const snapshot = await activePorts.activeInput.recover(
        { workflowId: normalizedWorkflowID },
        controller.signal,
      );
      if (controller.signal.aborted) return false;
      return applyActiveInputSnapshot(snapshot, "ready", stateRef.current.inputEditRevision);
    } catch (error) {
      if (!controller.signal.aborted) {
        dispatch({
          type: "workflow_view_failed",
          error: errorText(error),
          failure: operationFailureFromError(error),
        });
      }
      return false;
    } finally {
      if (controllers.current.activeInput === controller) delete controllers.current.activeInput;
    }
  };

  const selectSource = (value: string) => {
    cancelPreparation();
    dispatch({ type: "draft_changed", value });
  };

  const executePreparation = async (
    operation: "prepare" | "reset" | "candidate",
    sourcePath: string,
    intent: PreparationIntent,
    controls: Readonly<{ confirmBDMVRescan: boolean }>,
    commandRevision: number,
    correlationID: string,
    controller: AbortController,
    update: PendingInputUpdate,
    releaseID = "",
  ): Promise<boolean> => {
    try {
      const input = preparationInputForWorkflow(sourcePath, intent, controls.confirmBDMVRescan);
      const pendingPlaylist = workflowView.current?.workflow.requiredActions?.some(
        (action) => action.kind === "select_playlist" && action.status === "pending",
      );
      let current: ReleaseWorkflowCurrent | null;
      if (operation === "candidate") {
        const previous = workflowView.current;
        if (!previous?.release || previous.release.release.Source.SourcePath !== sourcePath) {
          throw new Error("Blu-ray candidate selection requires the current prepared workflow.");
        }
        dispatch({ type: "active_input_loading" });
        const commandID = `workflow-candidate-${Date.now().toString(36)}-${previous.workflow.revision.toString(36)}`;
        const candidateInput = workflowPrepareInput({
          ...input,
          Instructions: { ...input.Instructions, BlurayReleaseID: releaseID },
          Force: true,
        });
        current = await continueBackendGoal(
          previous,
          "input_ready",
          {
            ...(update.correctionDirty
              ? { correctionPatch: correctionPatchFor(previous, intent, update) }
              : {}),
            preparation: preparationWithoutFactCorrections(candidateInput),
            trackerIds: [...update.selectedTrackers],
          },
          commandID,
          controller.signal,
        );
      } else if (operation === "reset") {
        const previous = workflowView.current;
        if (!previous?.release || previous.release.release.Source.SourcePath !== sourcePath) {
          throw new Error("Reset requires the current prepared workflow.");
        }
        current = await startBackendWorkflow({ ...input, Force: true }, update, {
          correlationID,
          controller,
        });
      } else if (intent.playlist.Set && pendingPlaylist && workflowView.current) {
        dispatch({ type: "active_input_loading" });
        const commandID = `workflow-playlist-${Date.now().toString(36)}-${workflowView.current.workflow.revision.toString(36)}`;
        const playlistInput = workflowPrepareInput(input);
        current = await continueBackendGoal(
          workflowView.current,
          "input_ready",
          {
            ...(update.correctionDirty
              ? {
                  correctionPatch: correctionPatchFor(workflowView.current, intent, update),
                }
              : {}),
            preparation: preparationWithoutFactCorrections(playlistInput),
            trackerIds: [...update.selectedTrackers],
          },
          commandID,
          controller.signal,
        );
      } else {
        current = await startBackendWorkflow(input, update, { correlationID, controller });
      }
      if (!current) {
        throw (
          lastWorkflowError.current || new Error("Canonical workflow preparation did not complete.")
        );
      }
      if (dispatchPlaylistAction(current, sourcePath, commandRevision, correlationID)) {
        publishWorkflowCurrent(current, "ready");
        return false;
      }
      const trackerInputAnswers = Object.fromEntries(
        Object.entries(update.trackerInputAnswers).filter(([tracker]) =>
          update.selectedTrackers.includes(tracker),
        ),
      );
      const hasTrackerInputAnswers = Object.values(trackerInputAnswers).some(
        (answers) => Object.keys(answers).length > 0,
      );
      let trackerInputsAccepted = false;
      if (hasTrackerInputAnswers) {
        if (!current.inputReadiness || !current.release) {
          throw new Error(
            "Fact corrections were saved, but tracker Input answers need refreshed readiness.",
          );
        }
        const commandID = `workflow-input-answers-${Date.now().toString(36)}-${current.workflow.revision.toString(36)}`;
        current = await continueBackendGoal(
          current,
          "input_ready",
          { trackerInputAnswers },
          commandID,
          controller.signal,
        );
        trackerInputsAccepted = true;
      }
      const preview = metadataPreviewFromWorkflow(current);
      if (!preview) throw new Error("Workflow release snapshot is unavailable.");
      if (
        !applyActiveInputSnapshot(
          activeSnapshotWithCurrent(current),
          "ready",
          update.inputEditRevision,
          false,
          intent,
          sourcePath,
          update.selectedTrackers,
          trackerInputsAccepted,
        )
      ) {
        return false;
      }
      return !controller.signal.aborted;
    } catch (error) {
      if (!controller.signal.aborted) {
        dispatch({
          type: "preparation_failed",
          sourcePath,
          commandRevision,
          correlationID,
          error: errorText(error),
          failure: operationFailureFromError(error),
        });
      }
      return false;
    } finally {
      if (controllers.current.preparation === controller) delete controllers.current.preparation;
    }
  };

  const runPreparationFor = async (
    operation: "prepare" | "reset",
    requestedSource: string,
    requestedIntent: PreparationIntent,
    controls = { confirmBDMVRescan: false },
  ): Promise<boolean> => {
    if (activeAuthority.current.state === "recovering" && !hasOpaqueRecoveringInput()) return false;
    const sourcePath = requestedSource.trim();
    if (!sourcePath) return false;
    if (sourcePath !== state.selectedSource) {
      dispatch({ type: "draft_changed", value: sourcePath });
    }
    abortController("preparation");
    abortController("workflow");
    abortWorkflowFacets();
    const controller = new AbortController();
    controllers.current.preparation = controller;
    const commandRevision = Math.max(preparationRevision.current + 1, state.commandRevision + 1);
    preparationRevision.current = commandRevision;
    const correlationID = `preparation-${Date.now().toString(36)}-${commandRevision.toString(36)}`;
    const intent = cloneIntent(requestedIntent);
    const existingSource = sourcePath === state.selectedSource;
    const sourceChanged = Boolean(state.selectedSource) && !existingSource;
    const inputEditRevision = existingSource ? state.inputEditRevision : 0;
    const update: PendingInputUpdate = {
      inputEditRevision,
      correctionDirty: existingSource ? state.correctionDirty : false,
      resetFields: existingSource ? state.correctionResetFields.map((field) => ({ ...field })) : [],
      confirmFields: existingSource
        ? state.correctionConfirmFields.map((field) => ({ ...field }))
        : [],
      valueFields: existingSource ? state.correctionValueFields.map((field) => ({ ...field })) : [],
      trackerInputAnswers: sourceChanged
        ? {}
        : Object.fromEntries(
            Object.entries(state.trackerInputAnswers).map(([tracker, answers]) => [
              tracker,
              { ...answers },
            ]),
          ),
      selectedTrackers: sourceChanged
        ? [...normalizedDefaultTrackers]
        : [...state.selectedTrackers],
    };
    lastPreparation.current = { operation, sourcePath, intent };
    dispatch({
      type: "preparation_started",
      sourcePath,
      commandRevision,
      inputEditRevision,
      correlationID,
      intent,
    });

    return executePreparation(
      operation,
      sourcePath,
      intent,
      controls,
      commandRevision,
      correlationID,
      controller,
      update,
    );
  };

  const runPreparation = (operation: "prepare" | "reset") => {
    const sourcePath = state.sourceDraft.trim() || state.selectedSource;
    const intent =
      state.selectedSource && sourcePath !== state.selectedSource
        ? emptyPreparationIntent()
        : state.preparationIntent;
    return runPreparationFor(operation, sourcePath, intent);
  };

  const selectCandidate = async (releaseID: string): Promise<boolean> => {
    const sourcePath = state.selectedSource.trim();
    const candidateID = releaseID.trim();
    if (!sourcePath || !candidateID) return false;
    abortController("preparation");
    abortWorkflowFacets();
    const controller = new AbortController();
    controllers.current.preparation = controller;
    const commandRevision = Math.max(preparationRevision.current + 1, state.commandRevision + 1);
    preparationRevision.current = commandRevision;
    const correlationID = `preparation-${Date.now().toString(36)}-${commandRevision.toString(36)}`;
    const intent = cloneIntent(state.preparationIntent);
    const update: PendingInputUpdate = {
      inputEditRevision: state.inputEditRevision,
      correctionDirty: state.correctionDirty,
      resetFields: state.correctionResetFields.map((field) => ({ ...field })),
      confirmFields: state.correctionConfirmFields.map((field) => ({ ...field })),
      valueFields: state.correctionValueFields.map((field) => ({ ...field })),
      trackerInputAnswers: Object.fromEntries(
        Object.entries(state.trackerInputAnswers).map(([tracker, answers]) => [
          tracker,
          { ...answers },
        ]),
      ),
      selectedTrackers: [...state.selectedTrackers],
    };
    dispatch({
      type: "preparation_started",
      sourcePath,
      commandRevision,
      inputEditRevision: state.inputEditRevision,
      correlationID,
      intent,
    });
    return executePreparation(
      "candidate",
      sourcePath,
      intent,
      { confirmBDMVRescan: false },
      commandRevision,
      correlationID,
      controller,
      update,
      candidateID,
    );
  };

  const beginWorkflow = (facet: WorkflowFacet, unavailableReason = ""): WorkflowCommand | null => {
    if (controllers.current[facet]) return null;
    const revision = Math.max(
      (workflowRevisions.current[facet] || 0) + 1,
      state[facet].revision + 1,
    );
    workflowRevisions.current[facet] = revision;
    dispatch({ type: "workflow_started", facet, sessionRevision: state.sessionRevision, revision });
    if (!state.release || unavailableReason) {
      dispatch({
        type: "workflow_failed",
        facet,
        sessionRevision: state.sessionRevision,
        revision,
        error: unavailableReason || "Prepare the selected source first.",
      });
      return null;
    }
    const controller = new AbortController();
    controllers.current[facet] = controller;
    return {
      controller,
      release: { ...state.release },
      sessionRevision: state.sessionRevision,
      revision,
    };
  };

  const failWorkflow = (facet: WorkflowFacet, command: WorkflowCommand, error: unknown) => {
    if (!command.controller.signal.aborted) {
      const fallbackMessage: Readonly<Record<WorkflowFacet, string>> = {
        screenshots: "Screenshot request failed. Retry the request.",
        menuImages: "DVD menu image request failed. Retry the request.",
        uploadedImages: "Image hosting request failed. Retry the request.",
        descriptions: "Description request failed. Retry the request.",
      };
      dispatch({
        type: "workflow_failed",
        facet,
        sessionRevision: command.sessionRevision,
        revision: command.revision,
        error: operationFailureFromError(error)?.Message || fallbackMessage[facet],
      });
    }
  };

  const finishWorkflow = (facet: WorkflowFacet, command: WorkflowCommand) => {
    if (controllers.current[facet] === command.controller) delete controllers.current[facet];
  };

  const screenshotCommandCallbacks = (expectsMediaOperation = false): WorkflowCommandCallbacks => ({
    onAbort: (authority) =>
      setScreenshotCommand((command) =>
        command?.commandID === authority.commandID ? null : command,
      ),
    onError: (error, authority) =>
      setScreenshotCommand({
        ...authority,
        status: "error",
        message:
          operationFailureFromError(error)?.Message ||
          "Screenshot request failed. Retry the request.",
      }),
    onStart: (authority) => {
      dispatch({ type: "screenshot_command_started" });
      setScreenshotCommand({
        ...authority,
        status: "running",
        message: "",
      });
    },
    onSuccess: (current, authority) => {
      const operation = current.operation;
      if (
        expectsMediaOperation &&
        operation?.operation === "media" &&
        isFailedWorkflowOperation(operation)
      ) {
        setScreenshotCommand({
          ...authority,
          operationID: operation.id,
          operationSequence: operation.sequence,
          workflowID: current.workflow.id,
          workflowRevision: current.workflow.revision,
          status: "error",
          message:
            (operation.failures || []).map((failure) => failure.failure.Message).join(" ") ||
            operation.message ||
            `Screenshot operation ${operation.status}.`,
        });
        return;
      }
      setScreenshotCommand(null);
    },
  });

  const loadScreenshotPlan = async (): Promise<boolean> => {
    setScreenshotCommand(null);
    const command = beginWorkflow("screenshots", access.screenshots.reason);
    if (!command || !workflowView.current) return false;
    try {
      const workflowPlan = await activePorts.workflow.mediaPlan(
        workflowView.current.workflow.id,
        command.controller.signal,
      );
      if (command.controller.signal.aborted) return false;
      const selectedArtifactIDs = (workflowView.current.media?.artifacts || [])
        .filter((artifact) => artifact.kind === "screenshot" && artifact.selected)
        .sort((left, right) => (left.order || 0) - (right.order || 0))
        .map((artifact) => artifact.id);
      const plan: ScreenshotPlan = {
        SourcePath: workflowView.current.release?.release.Source.SourcePath || "",
        DiscType: workflowPlan.discType || "",
        Discs: (workflowPlan.discs || []).map((disc) => ({
          DiscID: disc.discId,
          DiscName: disc.discName,
          DurationSeconds: disc.durationSeconds,
          FrameRate: disc.frameRate,
          SuggestedSelections: [...(disc.suggestedSelections || [])],
        })),
        DurationSeconds: workflowPlan.durationSeconds,
        FrameRate: workflowPlan.frameRate,
        SuggestedSelections: [...(workflowPlan.suggestedSelections || [])],
        ExistingScreenshots: [],
        ExistingTrackerScreenshots: [],
        FinalSelections: [],
        TrackerImageLinks: [],
        PreviewImages: [],
        MetadataTimestamp: workflowPlan.createdAt,
        RequiresManualFrames: (workflowPlan.suggestedSelections || []).length === 0,
      };
      dispatch({
        type: "screenshots_loaded",
        sessionRevision: command.sessionRevision,
        revision: command.revision,
        plan,
        reseedDrafts: true,
        finalSelectionArtifactIDs: selectedArtifactIDs,
      });
      return !command.controller.signal.aborted;
    } catch (error) {
      failWorkflow("screenshots", command, error);
      return false;
    } finally {
      finishWorkflow("screenshots", command);
    }
  };

  const generateScreenshots = async (
    purpose: ScreenshotPurpose,
    selections?: readonly ScreenshotSelection[],
  ): Promise<boolean> => {
    if (workflowView.current && purpose === "preview") {
      const requested = selections ?? state.screenshots.selections;
      const selection = requested[0];
      if (!selection) return false;
      return previewWorkflowFrame(selection.DiscID || "", selection.TimestampSeconds);
    }
    if (workflowView.current) {
      const requested = [...(selections ?? state.screenshots.selections)].map((selection) => ({
        ...selection,
        DiscID: selection.DiscID || "",
      }));
      return runBackendWorkflow(
        (current, commandID, signal) =>
          continueBackendGoal(
            current,
            "media_ready",
            {
              media: {
                screenshotCount: requested.length,
                purpose,
                selections: requested,
                captureDvdMenus: false,
                maxDvdMenuItems: 0,
              },
            },
            commandID,
            signal,
          ),
        screenshotCommandCallbacks(true),
      );
    }
    return false;
  };

  const persistFinalScreenshotArtifacts = async (artifactIDs: readonly string[]) => {
    const normalizedArtifactIDs = Array.from(
      new Set(artifactIDs.map((artifactID) => artifactID.trim()).filter(Boolean)),
    );
    dispatch({
      type: "screenshot_final_artifacts_changed",
      artifactIDs: normalizedArtifactIDs,
    });
    if (!workflowView.current?.media || normalizedArtifactIDs.length === 0) return false;
    return runBackendWorkflow(
      (current, commandID, signal) =>
        activePorts.workflow.reorderMedia(current, normalizedArtifactIDs, commandID, signal),
      screenshotCommandCallbacks(),
    );
  };

  const removeMediaArtifacts = async (
    artifactIDs: readonly string[],
    callbacks: WorkflowCommandCallbacks = {},
  ) => {
    const normalizedArtifactIDs = Array.from(
      new Set(artifactIDs.map((artifactID) => artifactID.trim()).filter(Boolean)),
    );
    if (normalizedArtifactIDs.length === 0) return false;
    if (!workflowView.current?.media) return false;
    return runBackendWorkflow(
      (current, commandID, signal) =>
        activePorts.workflow.deleteMedia(current, normalizedArtifactIDs, commandID, signal),
      callbacks,
    );
  };

  const previewWorkflowFrame = async (
    discID: string,
    timestampSeconds: number,
  ): Promise<boolean> => {
    setScreenshotCommand(null);
    const command = beginWorkflow("screenshots", access.screenshots.reason);
    if (!command || !workflowView.current) return false;
    try {
      const preview = await activePorts.workflow.previewFrame(
        workflowView.current,
        discID,
        timestampSeconds,
        `preview-${workflowView.current.workflow.id}-${workflowView.current.workflow.revision}-${discID || "single"}-${timestampSeconds}`,
        command.controller.signal,
      );
      if (command.controller.signal.aborted) return false;
      dispatch({
        type: "screenshot_previewed",
        sessionRevision: command.sessionRevision,
        revision: command.revision,
        image: preview.contentUrl,
      });
      return !command.controller.signal.aborted;
    } catch (error) {
      failWorkflow("screenshots", command, error);
      return false;
    } finally {
      finishWorkflow("screenshots", command);
    }
  };

  const loadMenuImages = async (): Promise<boolean> => {
    const command = beginWorkflow("menuImages", access.menuImages.reason);
    if (!command || !workflowView.current) return false;
    try {
      const previews = (workflowView.current.media?.artifacts || [])
        .filter((artifact) => artifact.kind === "dvd_menu")
        .map((artifact, index) => ({
          image: {
            artifactID: artifact.id,
            discID: artifact.discId,
            discName: artifact.discName,
            index: artifact.index ?? index,
            timestampSeconds: artifact.timestampSeconds || 0,
            purpose: "menu" as const,
            width: artifact.width || 0,
            height: artifact.height || 0,
            sizeBytes: artifact.sizeBytes || 0,
          },
          contentURL: activePorts.workflow.mediaURL(workflowView.current!, artifact.id),
        }));
      dispatch({
        type: "menu_images_loaded",
        sessionRevision: command.sessionRevision,
        revision: command.revision,
        images: previews,
      });
      return !command.controller.signal.aborted;
    } catch (error) {
      failWorkflow("menuImages", command, error);
      return false;
    } finally {
      finishWorkflow("menuImages", command);
    }
  };

  const loadUploadedImages = async (): Promise<boolean> => {
    const command = beginWorkflow("uploadedImages", access.uploadedImages.reason);
    if (!command || !workflowView.current) return false;
    dispatch({
      type: "uploaded_images_progress_reset",
      sessionRevision: command.sessionRevision,
      revision: command.revision,
      correlationID: "",
    });
    try {
      const media = workflowView.current.media;
      const candidates = (media?.artifacts || [])
        .filter(
          (artifact) =>
            artifact.selected && (artifact.kind === "screenshot" || artifact.kind === "dvd_menu"),
        )
        .map((artifact, index) => ({
          image: {
            artifactID: artifact.id,
            discID: artifact.discId,
            discName: artifact.discName,
            index: artifact.index ?? index,
            timestampSeconds: artifact.timestampSeconds || 0,
            purpose: artifact.purpose as ScreenshotPurpose,
            width: artifact.width || 0,
            height: artifact.height || 0,
            sizeBytes: artifact.sizeBytes || 0,
          },
          contentURL: activePorts.workflow.mediaURL(workflowView.current!, artifact.id),
        }));
      const uploaded = (media?.artifacts || [])
        .filter((artifact) => artifact.kind === "hosted_image")
        .map((artifact) => ({
          artifactID: artifact.id,
          host: artifact.host || "",
          url: artifact.url || "",
          sizeBytes: artifact.sizeBytes || 0,
          uploadedAt: media?.createdAt || "",
        }));
      const failures: UploadImageHostFailure[] = (media?.hostAttempts || []).flatMap((attempt) =>
        (attempt.failures || []).map((failure) => ({
          Host: attempt.host,
          UsageScope: "workflow",
          Trackers: failure.trackerId ? [failure.trackerId] : [],
          Message: failure.failure.Message,
        })),
      );
      dispatch({
        type: "uploaded_images_loaded",
        sessionRevision: command.sessionRevision,
        revision: command.revision,
        candidates,
        uploaded,
        failures,
        failedHosts: media?.failedHosts || [],
      });
      return !command.controller.signal.aborted;
    } catch (error) {
      failWorkflow("uploadedImages", command, error);
      return false;
    } finally {
      finishWorkflow("uploadedImages", command);
    }
  };

  const descriptionInstructions = (current: ReleaseWorkflowCurrent): DescriptionInstructions => ({
    questionnaireAnswers: state.questionnaireAnswers,
    options: {
      RunLogLevel: state.uploadOptions.runLogLevel,
      Screens: workflowDescriptionScreenshotCount(current),
      NoSeed: uploadOptions.noSeed,
      AudioAnalysis: false,
      AudioTracks: "primary",
      AudioImages: "both",
      SkipAutoTorrent: false,
      OnlyID: false,
      KeepFolder: false,
      KeepImages: false,
      CaptureDVDMenus: false,
      InteractionMode: "interactive",
    },
    imageHost: workflowDescriptionImageHostOverrides(current.media?.failedHosts || []),
    templateVersion: "workflow-v1",
  });

  const loadDescriptions = async (): Promise<boolean> => {
    if (!workflowView.current?.media) return false;
    if (workflowView.current.descriptions) return true;
    const completed = await runBackendWorkflow((current, commandID, signal) =>
      continueBackendGoal(
        current,
        "descriptions_ready",
        { descriptions: descriptionInstructions(current) },
        `${commandID}-descriptions`,
        signal,
      ),
    );
    if (completed) dispatch({ type: "description_dirty_cleared" });
    return completed;
  };

  // Unedited groups have no local raw entry; they keep the backend artifact source.
  const descriptionSource = (key: string): string =>
    state.descriptions.rawByGroup[key] ??
    workflowView.current?.descriptions?.descriptions.find((group) => group.groupKey === key)
      ?.source ??
    "";

  const renderDescription = async (groupKey: string): Promise<boolean> => {
    const command = beginWorkflow("descriptions", access.descriptions.reason);
    if (!command) return false;
    const key = groupKey.trim();
    const inputRevision = state.descriptions.inputRevision;
    try {
      const html = await activePorts.descriptions.render(
        descriptionSource(key),
        command.controller.signal,
      );
      if (command.controller.signal.aborted) return false;
      dispatch({
        type: "description_rendered",
        sessionRevision: command.sessionRevision,
        revision: command.revision,
        inputRevision,
        groupKey: key,
        html,
      });
      return !command.controller.signal.aborted;
    } catch (error) {
      failWorkflow("descriptions", command, error);
      return false;
    } finally {
      finishWorkflow("descriptions", command);
    }
  };

  const saveDescription = async (groupKey: string, reset: boolean): Promise<boolean> => {
    const key = groupKey.trim();
    if (!key || !workflowView.current?.descriptions) return false;
    const completed = await runBackendWorkflow((current, commandID, signal) =>
      reset
        ? activePorts.workflow.resetDescriptionOverride(current, key, commandID, signal)
        : activePorts.workflow.saveDescriptionOverride(
            current,
            key,
            descriptionSource(key),
            commandID,
            signal,
          ),
    );
    if (completed) {
      dispatch({
        type: "description_dirty_cleared",
        groupKey: key,
        notice: reset ? "Description reset." : "Description saved.",
      });
    }
    return completed;
  };

  // Selected trackers are pre-dupe UI state; retained backend evidence owns the exact downstream set.
  const backendResolvedUploadIntent = (current: ReleaseWorkflowCurrent): WorkflowIntent => ({
    noSeed: uploadOptions.noSeed,
    media:
      !current.media && !requirements.needsImages
        ? { screenshotCount: 0, purpose: "final", captureDvdMenus: false }
        : undefined,
    descriptions: descriptionInstructions(current),
  });

  const hasDryRunCandidate = (current: ReleaseWorkflowCurrent) => {
    const exclusions = current.workflow.submissionExclusions || [];
    if (exclusions.length === 0) return true;
    if (state.selectedTrackers.length === 0) return false;
    const excluded = new Set(exclusions.map((item) => item.trackerId));
    return state.selectedTrackers.some((tracker) => !excluded.has(tracker));
  };

  const hasUploadEligibleTracker = (current: ReleaseWorkflowCurrent) => {
    const excluded = new Set(
      (current.workflow.submissionExclusions || []).map((item) => item.trackerId),
    );
    return canExecuteUpload(
      current.continuation?.trackerOutcomes || [],
      state.selectedTrackers,
      excluded,
      current.dryRun || null,
    );
  };

  const runDryRun = async (): Promise<boolean> => {
    if (!workflowView.current || !hasDryRunCandidate(workflowView.current)) return false;
    return runBackendWorkflow((current, commandID, signal) =>
      continueBackendGoal(
        current,
        "dry_run",
        backendResolvedUploadIntent(current),
        commandID,
        signal,
      ),
    );
  };

  const executeExactUpload = async (): Promise<boolean> => {
    if (!mutationsAllowed) return false;
    if (
      !workflowView.current ||
      controllers.current.workflow ||
      !hasUploadEligibleTracker(workflowView.current)
    ) {
      return false;
    }
    const controller = new AbortController();
    controllers.current.workflow = controller;
    dispatch({ type: "active_input_loading" });
    const commandID = `workflow-upload-${Date.now().toString(36)}-${workflowView.current.workflow.revision.toString(36)}`;
    try {
      let current = workflowView.current;
      if (!current.dryRun) {
        current = await continueBackendGoal(
          current,
          "dry_run",
          backendResolvedUploadIntent(current),
          `${commandID}-review`,
          controller.signal,
        );
      }
      if (!current.dryRun) {
        throw new Error("Exact upload dry run is unavailable.");
      }
      if (!hasUploadEligibleTracker(current)) {
        acceptWorkflowCurrent(current);
        return false;
      }
      const uploaded = await continueBackendGoal(
        current,
        "uploaded",
        backendResolvedUploadIntent(current),
        `${commandID}-execute`,
        controller.signal,
      );
      releaseWorkflowController(controller);
      if (controller.signal.aborted) return false;
      acceptWorkflowCurrent(uploaded);
      return true;
    } catch (error) {
      releaseWorkflowController(controller);
      if (!controller.signal.aborted) failBackendWorkflow(error);
      return false;
    } finally {
      releaseWorkflowController(controller);
    }
  };

  const workflowDupeAssessment = workflowView.current?.dupes || null;
  const workflowDupeSelection =
    workflowView.current?.projections?.projections.map((projection) => projection.trackerId) || [];
  const duplicateAssessmentCurrent = sameNames(workflowDupeSelection, state.selectedTrackers);
  const duplicateOperation =
    workflowView.current?.operation?.operation === "duplicate_check"
      ? workflowView.current.operation
      : null;
  const duplicateStartPending = isActiveWorkflowOperation(duplicateOperation ?? undefined);
  const duplicatesReady =
    !duplicateStartPending &&
    duplicateAssessmentCurrent &&
    workflowDupeAssessment?.status === "completed";
  const projectedTrackers = workflowView.current?.projections?.projections || [];
  const requirements: TrackerWorkflowRequirements = {
    needsImages: projectedTrackers.some(
      (projection) =>
        projection.artifacts.screenshotCount > 0 ||
        projection.artifacts.dvdMenuCount > 0 ||
        projection.artifacts.imageHosting,
    ),
    needsDescriptions: projectedTrackers.some((projection) => projection.artifacts.description),
  };
  const access: Readonly<Record<ReleaseRoute, RouteAccess>> =
    state.activeInput.state === "recovering"
      ? {
          input: { available: true, reason: "" },
          trackerData: { available: false, reason: "Resolve recovery actions first." },
          audioAnalysis: { available: false, reason: "Resolve recovery actions first." },
          duplicates: { available: false, reason: "Resolve recovery actions first." },
          screenshots: { available: false, reason: "Resolve recovery actions first." },
          menuImages: { available: false, reason: "Resolve recovery actions first." },
          uploadedImages: { available: false, reason: "Resolve recovery actions first." },
          descriptions: { available: false, reason: "Resolve recovery actions first." },
          upload: { available: false, reason: "Resolve recovery actions first." },
        }
      : routeAccess(
          workflowView.current?.continuation,
          Boolean(state.preview?.TrackerData?.length),
          requirements,
          Boolean(
            workflowView.current?.release?.release.Media?.Tracks?.some(
              (track) => track.Kind === "audio",
            ),
          ),
        );
  const workflowMedia = workflowView.current?.media;
  const workflowMediaURL = (artifactID: string) =>
    workflowView.current ? activePorts.workflow.mediaURL(workflowView.current, artifactID) : "";
  const selectedScreenshotIDs = (workflowMedia?.artifacts || [])
    .filter((artifact) => artifact.kind === "screenshot" && artifact.selected)
    .sort((left, right) => (left.order || 0) - (right.order || 0))
    .map((artifact) => artifact.id);
  const workflowMenuImages = (workflowMedia?.artifacts || [])
    .filter((artifact) => artifact.kind === "dvd_menu")
    .sort((left, right) => (left.order || 0) - (right.order || 0))
    .map((artifact, index) => ({
      image: {
        artifactID: artifact.id,
        discID: artifact.discId,
        discName: artifact.discName,
        index: artifact.index ?? index,
        timestampSeconds: artifact.timestampSeconds || 0,
        purpose: "menu" as const,
        width: artifact.width || 0,
        height: artifact.height || 0,
        sizeBytes: artifact.sizeBytes || 0,
      },
      contentURL: workflowMediaURL(artifact.id),
    }));
  const workflowUploadCandidates = useMemo(() => {
    const current = workflowView.current;
    return (current?.media?.artifacts || [])
      .filter(
        (artifact) =>
          artifact.selected && (artifact.kind === "screenshot" || artifact.kind === "dvd_menu"),
      )
      .map((artifact, index) => ({
        image: {
          artifactID: artifact.id,
          discID: artifact.discId,
          discName: artifact.discName,
          index: artifact.index ?? index,
          timestampSeconds: artifact.timestampSeconds || 0,
          purpose: artifact.purpose as ScreenshotPurpose,
          width: artifact.width || 0,
          height: artifact.height || 0,
          sizeBytes: artifact.sizeBytes || 0,
        },
        contentURL: current ? activePorts.workflow.mediaURL(current, artifact.id) : "",
      }));
  }, [activePorts.workflow, workflowView]);
  useEffect(() => {
    dispatch({
      type: "workflow_upload_candidates_changed",
      candidates: workflowUploadCandidates,
    });
  }, [workflowUploadCandidates]);
  const workflowUploadedImages = (workflowMedia?.artifacts || [])
    .filter((artifact) => artifact.kind === "hosted_image")
    .map((artifact) => ({
      artifactID: artifact.id,
      host: artifact.host || "",
      url: artifact.url || "",
      sizeBytes: artifact.sizeBytes || 0,
      uploadedAt: workflowMedia?.createdAt || "",
    }));
  const workflowHostFailures: UploadImageHostFailure[] = (
    workflowMedia?.hostAttempts || []
  ).flatMap((attempt) =>
    (attempt.failures || []).map((failure) => ({
      Host: attempt.host,
      UsageScope: "workflow",
      Trackers: failure.trackerId ? [failure.trackerId] : [],
      Message: failure.failure.Message,
    })),
  );

  const workflowUploadOperation = workflowView.current?.operation?.operation;
  const workflowDryRunStatus =
    workflowView.status === "running" && workflowUploadOperation === "upload_dry_run"
      ? "running"
      : workflowView.current?.dryRun
        ? "ready"
        : "idle";
  const workflowUploadStatus =
    workflowView.status === "running" && workflowUploadOperation === "upload_execute"
      ? "running"
      : workflowView.current?.uploadResult
        ? "ready"
        : workflowView.current?.workflow.status === "completed" &&
            (workflowView.current.workflow.submissionExclusions?.length || 0) > 0
          ? "ready"
          : "idle";
  const preparedRelease = workflowView.current?.release?.release;
  const preparedAudioTracks = (preparedRelease?.Media?.Tracks || []).filter(
    (track) => track.Kind === "audio",
  );
  const retainedAudioAnalysis = (() => {
    const current = workflowView.current;
    const result = current?.audioAnalysis;
    const reference = current?.workflow.audioAnalysis;
    if (
      !current?.release ||
      !result ||
      !reference ||
      reference.id !== result.id ||
      reference.revision !== result.revision ||
      result.release.Generation !== current.release.release.Generation ||
      result.release.SourcePath !== current.release.release.Source.SourcePath
    ) {
      return null;
    }
    return result;
  })();
  const activeWorkflowOperation = isActiveWorkflowOperation(workflowView.current?.operation)
    ? workflowView.current?.operation || null
    : null;
  const latestWorkflowOperation = workflowView.current?.operation;
  const latestWorkflowOperationIsActive =
    latestWorkflowOperation?.status === "queued" || latestWorkflowOperation?.status === "running";
  const audioOperation =
    latestWorkflowOperation?.operation === "analyze_audio" &&
    (latestWorkflowOperationIsActive ||
      (latestWorkflowOperation.resultRevision ?? latestWorkflowOperation.revision) >=
        (workflowView.current?.workflow.revision ?? 0))
      ? latestWorkflowOperation
      : null;
  const retainedAudioMatchesOperation = Boolean(
    audioOperation && retainedAudioAnalysis?.attemptId === audioOperation.id,
  );
  const retainedAudioError =
    retainedAudioAnalysis?.tracks
      .flatMap((track) => [
        track.failure?.message,
        ...track.artifacts.map((artifact) => artifact.failure?.message),
      ])
      .filter((message): message is string => Boolean(message))
      .join(" ") || "";
  const audioOperationError =
    (audioOperation?.failures || []).map((failure) => failure.failure.Message).join(" ") ||
    (audioOperation && isFailedWorkflowOperation(audioOperation)
      ? audioOperation.message || `Audio analysis ${audioOperation.status}.`
      : "");
  const workflowAudioFailure =
    workflowView.failure?.Operation === "analyze_audio" ? workflowView.failure : null;
  const audioCommandError =
    audioCommandFailure &&
    workflowView.current &&
    !commandFailureSuperseded(audioCommandFailure, workflowView.current)
      ? audioCommandFailure.message
      : "";
  const currentAudioError = retainedAudioMatchesOperation
    ? retainedAudioError ||
      audioOperationError ||
      workflowAudioFailure?.Message ||
      audioCommandError
    : audioOperation
      ? audioOperationError || workflowAudioFailure?.Message || audioCommandError
      : workflowAudioFailure?.Message || audioCommandError || retainedAudioError;
  const audioMutationBlockedReason =
    activeWorkflowOperation && !audioOperation
      ? `Another workflow operation (${activeWorkflowOperation.operation.replaceAll("_", " ")}) is running. Wait for it to finish before changing audio analysis.`
      : "";
  const audioAnalysisStatus = isActiveWorkflowOperation(audioOperation ?? undefined)
    ? "running"
    : (audioOperation && isFailedWorkflowOperation(audioOperation)) ||
        Boolean(workflowAudioFailure || audioCommandError) ||
        ((!audioOperation || retainedAudioMatchesOperation) &&
          retainedAudioAnalysis?.status === "failed")
      ? "error"
      : retainedAudioAnalysis
        ? "ready"
        : "idle";
  const currentScreenshotCommand =
    screenshotCommand &&
    workflowView.current &&
    screenshotCommand.workflowID === workflowView.current.workflow.id &&
    (screenshotCommand.status === "running" ||
      !commandFailureSuperseded(screenshotCommand, workflowView.current))
      ? screenshotCommand
      : null;
  const screenshotStatus =
    currentScreenshotCommand?.status === "running"
      ? "running"
      : currentScreenshotCommand?.status === "error"
        ? "error"
        : state.screenshots.status;
  const screenshotError =
    currentScreenshotCommand?.status === "error"
      ? currentScreenshotCommand.message
      : state.screenshots.status === "error"
        ? state.screenshots.error
        : "";
  const screenshotMutationBlockedReason =
    workflowView.status === "running" && screenshotStatus !== "running"
      ? activeWorkflowOperation
        ? `Another workflow operation (${activeWorkflowOperation.operation.replaceAll("_", " ")}) is running. Wait for it to finish before changing screenshots.`
        : "Another workflow operation is running. Wait for it to finish before changing screenshots."
      : "";
  const trackerInputAnswers = state.trackerInputAnswers;

  const runAudioAnalysis = (input: AudioAnalysisGenerateInput): Promise<boolean> =>
    runBackendWorkflow(
      (current, commandID, signal) => {
        const release = current.release?.release;
        if (!release) throw new Error("Prepare the release before generating audio analysis.");
        const instructions: AudioAnalysisInstructions = {
          release: {
            SourcePath: release.Source.SourcePath,
            Generation: release.Generation,
          },
          resourceId: input.resourceID,
          selection: input.selection,
          trackIds: [...input.trackIDs],
          variants: [...input.variants],
          profileVersion: "audio-analysis-v3",
          resourceLimits: input.resourceLimits ?? {
            decoderThreads: 2,
          },
        };
        return activePorts.workflow.analyzeAudio(current, instructions, commandID, signal);
      },
      {
        onError: (error, authority) =>
          setAudioCommandFailure({
            ...authority,
            message:
              operationFailureFromError(error)?.Message ||
              "Audio analysis could not start. Retry the request.",
          }),
        onStart: () => setAudioCommandFailure(null),
      },
    );

  const cancelAudioAnalysis = async (): Promise<boolean> => {
    const current = stateRef.current.workflowView.current;
    const operation = current?.operation;
    if (
      !current ||
      operation?.operation !== "analyze_audio" ||
      !isActiveWorkflowOperation(operation)
    ) {
      return false;
    }
    abortController("workflow");
    setAudioCommandFailure(null);
    const controller = new AbortController();
    controllers.current.workflow = controller;
    dispatch({ type: "active_input_loading" });
    const commandID = `audio-analysis-cancel-${operation.id}`;
    try {
      const canceled = await activePorts.workflow.cancelOperation(
        current.workflow.id,
        operation.id,
        controller.signal,
      );
      await awaitWorkflowOperationTerminal(current.workflow.id, canceled, controller.signal);
      const latest = await activePorts.workflow.current(current.workflow.id, controller.signal);
      if (controller.signal.aborted) return false;
      acceptWorkflowCurrent(latest);
      return true;
    } catch (error) {
      if (!controller.signal.aborted) {
        setAudioCommandFailure({
          ...backendCommandAuthority(commandID, current),
          message:
            operationFailureFromError(error)?.Message ||
            "Audio analysis could not be canceled. Retry the request.",
        });
        failBackendWorkflow(error);
      }
      return false;
    } finally {
      releaseWorkflowController(controller);
    }
  };

  const disableAudioAnalysis = async (): Promise<boolean> => {
    let current = stateRef.current.workflowView.current;
    if (!current) return false;
    abortController("workflow");
    setAudioCommandFailure(null);
    const controller = new AbortController();
    controllers.current.workflow = controller;
    dispatch({ type: "active_input_loading" });
    const commandID = timestampedCommandID("audio-analysis-disable", current.workflow.revision);
    try {
      const operation = current.operation;
      if (operation?.operation === "analyze_audio" && isActiveWorkflowOperation(operation)) {
        const canceled = await activePorts.workflow.cancelOperation(
          current.workflow.id,
          operation.id,
          controller.signal,
        );
        await awaitWorkflowOperationTerminal(current.workflow.id, canceled, controller.signal);
        current = await activePorts.workflow.current(current.workflow.id, controller.signal);
      }
      const disabled = await activePorts.workflow.setAudioAnalysisEnabled(
        current,
        false,
        commandID,
        controller.signal,
      );
      if (controller.signal.aborted) return false;
      acceptWorkflowCurrent(disabled);
      setAudioCommandFailure(null);
      return true;
    } catch (error) {
      if (!controller.signal.aborted) {
        setAudioCommandFailure({
          ...backendCommandAuthority(commandID, current),
          message:
            operationFailureFromError(error)?.Message ||
            "Audio analysis could not be disabled. Retry the request.",
        });
        failBackendWorkflow(error);
      }
      return false;
    } finally {
      releaseWorkflowController(controller);
    }
  };

  const session: ReleaseSession = {
    workflow: {
      view: workflowView,
      reload: reloadBackendWorkflow,
      begin: async (input) => Boolean(await startBackendWorkflow(input)),
      project: (trackers, instructions = {}) =>
        runBackendWorkflow((current, commandID, signal) =>
          continueBackendGoal(
            current,
            "trackers_assessed",
            { trackerIds: [...trackers], projectionInstructions: instructions },
            commandID,
            signal,
          ),
        ),
      preflight: () =>
        runBackendWorkflow((current, commandID, signal) =>
          continueBackendGoal(current, "trackers_assessed", {}, commandID, signal),
        ),
      checkDuplicates: (skipRemote = false) =>
        runBackendWorkflow((current, commandID, signal) =>
          continueBackendGoal(
            current,
            "duplicates_decided",
            { skipRemoteDuplicates: skipRemote },
            commandID,
            signal,
          ),
        ),
      decideDuplicates: (decisions: Readonly<Record<string, DupeDecision>>) =>
        runBackendWorkflow((current, commandID, signal) =>
          continueBackendGoal(
            current,
            "duplicates_decided",
            { duplicateDecisions: decisions },
            commandID,
            signal,
          ),
        ),
      captureMedia: (instructions: MediaCaptureInstructions) =>
        runBackendWorkflow((current, commandID, signal) =>
          continueBackendGoal(current, "media_ready", { media: instructions }, commandID, signal),
        ),
      generateDescriptions: (instructions: DescriptionInstructions) =>
        runBackendWorkflow((current, commandID, signal) =>
          continueBackendGoal(
            current,
            "descriptions_ready",
            { descriptions: instructions },
            commandID,
            signal,
          ),
        ),
      dryRunUploads: () =>
        runBackendWorkflow((current, commandID, signal) =>
          continueBackendGoal(
            current,
            "dry_run",
            backendResolvedUploadIntent(current),
            commandID,
            signal,
          ),
        ),
      executeUploads: () => executeExactUpload(),
      confirmAction: (action: RequiredAction, confirmed = true) => {
        if (action.kind === "reconcile_submission") {
          return reconcileRecoveryAction(action);
        }
        if (
          !runtimeInfoReady ||
          (action.kind !== "authorize_rules" && action.kind !== "resolve_tracker_preparation") ||
          (action.kind === "authorize_rules" && !confirmed)
        ) {
          return Promise.resolve(false);
        }
        return runBackendWorkflow((current, commandID, signal) =>
          continueBackendGoal(
            current,
            liveTest ? "dry_run" : "uploaded",
            backendResolvedUploadIntent(current),
            commandID,
            signal,
            {
              answers: [
                {
                  actionId: action.id,
                  workflowRevision: current.workflow.revision,
                  confirmed,
                },
              ],
            },
          ),
        );
      },
      retryFailedUploads: () => {
        if (!mutationsAllowed) return Promise.resolve(false);
        const result = workflowView.current?.uploadResult;
        if (!result) return Promise.resolve(false);
        const trackerIDs = result.results
          .filter((item) => item.submissionStatus === "failed")
          .map((item) => item.trackerId);
        if (trackerIDs.length === 0) return Promise.resolve(false);
        return runBackendWorkflow((current, commandID, signal) =>
          activePorts.workflow.retryFailedUploads(
            current,
            { id: result.id, revision: result.revision },
            trackerIDs,
            state.uploadOptions.noSeed,
            commandID,
            signal,
          ),
        );
      },
      retryClientInjections: () => {
        if (!mutationsAllowed) return Promise.resolve(false);
        const result = workflowView.current?.uploadResult;
        if (!result) return Promise.resolve(false);
        const trackerIDs = result.results
          .filter(
            (item) =>
              item.submissionStatus === "completed" &&
              item.clientInjectionStatus === "failed" &&
              item.clientFailureCode === "client_injection",
          )
          .map((item) => item.trackerId);
        if (trackerIDs.length === 0) return Promise.resolve(false);
        return runBackendWorkflow((current, commandID, signal) =>
          activePorts.workflow.retryClientInjections(
            current,
            { id: result.id, revision: result.revision },
            trackerIDs,
            commandID,
            signal,
          ),
        );
      },
      invalidateTrackers: (trackerIDs, reason) =>
        runBackendWorkflow((current, commandID, signal) =>
          activePorts.workflow.invalidateTrackers(current, trackerIDs, reason, commandID, signal),
        ),
    },
    identity: {
      view: {
        sessionRevision: state.sessionRevision,
        sourcePath: state.selectedSource,
        release: state.release,
        preview: state.preview,
      },
    },
    navigation: { view: { access }, open: (route) => access[route].available },
    input: {
      view: {
        sourceDraft: state.sourceDraft,
        selectedSource: state.selectedSource,
        status: state.preparation.status,
        error: state.preparation.error || workflowView.error,
        failure: state.preparation.failure,
        activeInput: state.activeInput,
        sourceVerification: state.sourceVerification,
        preparationDirty: state.preparationDirty,
        correctionDirty: state.correctionDirty,
        intent: state.preparationIntent,
        corrections: workflowView.current?.corrections || null,
        resetFields: state.correctionResetFields,
        confirmFields: state.correctionConfirmFields,
        trackerInputAnswers,
        selectedTrackers: state.selectedTrackers,
        preview: state.preview,
        release: workflowView.current?.release
          ? workflowViewValue(workflowView.current.release.release)
          : null,
        readiness: workflowView.current?.inputReadiness || null,
        trackerData: state.preview?.TrackerData || [],
        source: {
          discCount: workflowView.current?.release?.release.Source.Classification?.DiscCount || 0,
          discType: workflowView.current?.release?.release.Source.Classification?.DiscType || "",
        },
        playlist: state.playlist,
      },
      updateSourceDraft: (value) => dispatch({ type: "draft_changed", value }),
      selectSource,
      changeSourceLookupURL: (value) => dispatch({ type: "source_lookup_changed", value }),
      changeIdentity: (value) => dispatch({ type: "identity_changed", value }),
      changeMetadata: (value) => dispatch({ type: "metadata_changed", value }),
      changeReleaseName: (value) => dispatch({ type: "release_name_changed", value }),
      resetCorrection: (field) => dispatch({ type: "correction_reset", field }),
      confirmCorrection: (field) => dispatch({ type: "correction_confirmed", field }),
      changeTrackerInputAnswer: (tracker, key, value) =>
        dispatch({ type: "tracker_input_answered", tracker, key, value }),
      changeTrackerSourceID: (tracker, value) =>
        dispatch({ type: "tracker_source_id_changed", tracker, value }),
      changePreparationPolicy: (value) => dispatch({ type: "preparation_policy_changed", value }),
      changeClientSearch: (value) => dispatch({ type: "client_search_changed", value }),
      chooseTrackers: (trackers) => dispatch({ type: "trackers_chosen", trackers }),
      choosePlaylists: (playlists, useAll) =>
        dispatch({ type: "playlist_draft_changed", playlists, useAll }),
      confirmPlaylists: () => {
        if (
          !state.playlist.required ||
          !playlistSelectionComplete(state.playlist.candidates, state.playlist.selected) ||
          !state.preparation.correlationID
        ) {
          return Promise.resolve(false);
        }
        abortController("preparation");
        const controller = new AbortController();
        controllers.current.preparation = controller;
        const intent = cloneIntent({
          ...state.preparationIntent,
          playlist: {
            Set: true,
            Selected: [...state.playlist.selected],
            UseAll: state.playlist.useAll,
          },
        });
        const sourcePath = state.preparation.sourcePath;
        const commandRevision = state.commandRevision;
        const correlationID = state.preparation.correlationID;
        const operation = lastPreparation.current?.operation || "prepare";
        lastPreparation.current = { operation, sourcePath, intent };
        dispatch({
          type: "playlist_resumed",
          sourcePath,
          commandRevision,
          correlationID,
          intent,
        });
        return executePreparation(
          operation,
          sourcePath,
          intent,
          { confirmBDMVRescan: false },
          commandRevision,
          correlationID,
          controller,
          {
            inputEditRevision: state.inputEditRevision,
            correctionDirty: state.correctionDirty,
            resetFields: state.correctionResetFields.map((field) => ({ ...field })),
            confirmFields: state.correctionConfirmFields.map((field) => ({ ...field })),
            valueFields: state.correctionValueFields.map((field) => ({ ...field })),
            trackerInputAnswers: Object.fromEntries(
              Object.entries(state.trackerInputAnswers).map(([tracker, answers]) => [
                tracker,
                { ...answers },
              ]),
            ),
            selectedTrackers: [...state.selectedTrackers],
          },
        );
      },
      cancelPlaylistSelection: () => {
        abortController("preparation");
        dispatch({ type: "playlist_dismissed" });
      },
      cancelPreparation,
      prepareSource: (sourcePath, intent) => runPreparationFor("prepare", sourcePath, intent),
      openSource: (sourcePath) =>
        runPreparationFor("prepare", sourcePath, emptyPreparationIntent()),
      recoverLegacyWorkflow,
      close: releaseActiveInput,
      resetSource: (sourcePath, intent) => runPreparationFor("reset", sourcePath, intent),
      prepare: () => runPreparation("prepare"),
      reset: () => runPreparation("reset"),
      confirmBDMVRescan: () => {
        const retry = lastPreparation.current;
        if (!retry || state.preparation.failure?.Recovery !== "confirm")
          return Promise.resolve(false);
        return runPreparationFor(retry.operation, retry.sourcePath, retry.intent, {
          confirmBDMVRescan: true,
        });
      },
      selectCandidate,
    },
    duplicates: {
      view: {
        status:
          duplicateStartPending || workflowView.status === "running"
            ? "running"
            : duplicatesReady
              ? "ready"
              : workflowDupeAssessment?.status === "failed"
                ? "error"
                : "idle",
        assessment: duplicateAssessmentCurrent ? workflowDupeAssessment : null,
        projections: duplicateAssessmentCurrent ? workflowView.current?.projections || null : null,
        preflight: duplicateAssessmentCurrent ? workflowView.current?.preflight || null : null,
        completed:
          duplicateOperation?.completed ||
          workflowDupeAssessment?.results.filter((result) =>
            ["completed", "failed", "skipped"].includes(result.status),
          ).length ||
          0,
        total:
          duplicateOperation?.total ||
          workflowView.current?.projections?.projections.length ||
          state.selectedTrackers.length,
        ignoredTrackers: state.ignoredDupesFor,
        selectedTrackers: state.selectedTrackers,
        releaseNameOverrides: state.releaseNameOverrides,
        error: workflowView.failure?.Message || state.duplicatesError || "",
      },
      run: async () => {
        dispatch({ type: "job_command_started", kind: "duplicates" });
        if (!state.release || !access.duplicates.available || state.selectedTrackers.length === 0) {
          dispatch({
            type: "job_command_failed",
            kind: "duplicates",
            error:
              access.duplicates.reason || "Select at least one tracker to run duplicate checking.",
          });
          return false;
        }
        if (!workflowView.current) {
          dispatch({
            type: "job_command_failed",
            kind: "duplicates",
            error: "Release workflow duplicate checking is unavailable.",
          });
          return false;
        }
        const completed = await checkBackendDuplicates();
        if (!completed) {
          dispatch({
            type: "job_command_failed",
            kind: "duplicates",
            error: workflowView.failure?.Message || "Duplicate workflow did not complete.",
          });
        }
        return completed;
      },
      chooseTrackers: (trackers) => dispatch({ type: "trackers_chosen", trackers }),
      confirmReleaseName: (tracker, value) =>
        dispatch({ type: "release_name_confirmed", tracker, value }),
      acknowledgeReleaseName: async (tracker, acknowledged) => {
        const normalizedTracker = tracker.trim().toUpperCase();
        const current = workflowView.current;
        const projection = current?.projections?.projections.find(
          (candidate) => candidate.trackerId === normalizedTracker,
        );
        const action = [
          ...(current?.workflow.requiredActions || []),
          ...(projection?.requiredActions || []),
        ].find(
          (candidate) =>
            candidate.kind === "provide_tracker_input" &&
            candidate.trackerId === normalizedTracker &&
            candidate.status === (acknowledged ? "pending" : "resolved"),
        );
        const releaseName = (
          state.releaseNameOverrides[normalizedTracker] ??
          projection?.uploadReleaseName ??
          ""
        ).trim();
        if (!current || !projection || !action || (acknowledged && !releaseName)) return false;
        return runBackendWorkflow((latest, commandID, signal) =>
          continueBackendGoal(latest, "duplicates_decided", {}, commandID, signal, {
            answers: [
              {
                actionId: action.id,
                workflowRevision: latest.workflow.revision,
                ...(acknowledged ? { textValue: releaseName } : {}),
                confirmed: acknowledged,
              },
            ],
          }),
        );
      },
      overrideRules: async (tracker) => {
        const normalizedTracker = tracker.trim().toUpperCase();
        const current = workflowView.current;
        const projection = current?.projections?.projections.find(
          (candidate) => candidate.trackerId === normalizedTracker,
        );
        const action = [
          ...(current?.workflow.requiredActions || []),
          ...(projection?.requiredActions || []),
        ].find(
          (candidate) =>
            candidate.kind === "authorize_rules" &&
            candidate.trackerId === normalizedTracker &&
            candidate.status === "pending",
        );
        if (!current || !action) return false;
        return runBackendWorkflow((latest, commandID, signal) =>
          continueBackendGoal(latest, "duplicates_decided", {}, commandID, signal, {
            answers: [
              {
                actionId: action.id,
                workflowRevision: latest.workflow.revision,
                confirmed: true,
              },
            ],
          }),
        );
      },
      cancel: async () => {
        if (!workflowView.current) return false;
        return cancelBackendWorkflow("duplicate check canceled");
      },
      setIgnored: (tracker, ignored) => {
        const normalizedTracker = tracker.trim().toUpperCase();
        const result = workflowDupeAssessment?.results.find(
          (candidate) => candidate.trackerId === normalizedTracker,
        );
        if (!result) return;
        const inClient = result.matches?.some(
          (match) => match.reason?.trim().toLowerCase() === "in_client",
        );
        const hasReviewableEvidence =
          Boolean(result.matches?.length) ||
          (Boolean(result.search?.pages) && result.search?.complete === false);
        const canOverride =
          !inClient &&
          hasReviewableEvidence &&
          ["pending", "accepted", "ignored"].includes(result.decision);
        if (!canOverride) return;
        dispatch({ type: "dupe_ignore_changed", tracker: normalizedTracker, ignored });
        void runBackendWorkflow((current, commandID, signal) =>
          continueBackendGoal(
            current,
            "duplicates_decided",
            {
              duplicateDecisions: {
                [normalizedTracker]: ignored ? "ignored" : "accepted",
              },
            },
            commandID,
            signal,
          ),
        );
      },
    },
    audioAnalysis: {
      view: {
        available: access.audioAnalysis.available,
        enabled: workflowView.current?.workflow.audioAnalysisEnabled === true,
        status: audioAnalysisStatus,
        releaseGeneration: Number(preparedRelease?.Generation || 0),
        sourceLabel:
          workflowView.current?.release?.display.ReleaseName ||
          preparedRelease?.Naming.ReleaseName ||
          "Prepared source",
        sourceContext: [
          preparedRelease?.Source.Classification?.DiscType,
          preparedRelease?.Source.Classification?.Container,
        ]
          .filter(Boolean)
          .join(" · "),
        primaryTrackID: preparedRelease?.Media?.PrimaryAudioTrackID || "",
        tracks: preparedAudioTracks as readonly MediaTrackFacts[],
        result: retainedAudioAnalysis,
        completed: audioOperation?.completed || 0,
        total: audioOperation?.total || retainedAudioAnalysis?.tracks.length || 0,
        operationItems: audioOperation?.items || [],
        mutationBlockedReason: audioMutationBlockedReason,
        error: currentAudioError,
      },
      generate: runAudioAnalysis,
      retry: () => {
        if (!retainedAudioAnalysis) return Promise.resolve(false);
        return runAudioAnalysis({
          resourceID: retainedAudioAnalysis.resourceId,
          selection: retainedAudioAnalysis.selection,
          trackIDs: retainedAudioAnalysis.trackIds,
          variants: retainedAudioAnalysis.variants,
          resourceLimits: retainedAudioAnalysis.resourceLimits,
        });
      },
      cancel: cancelAudioAnalysis,
      disable: disableAudioAnalysis,
      artifactURL: (artifactID) =>
        workflowView.current && retainedAudioAnalysis
          ? activePorts.workflow.audioAnalysisURL(
              workflowView.current,
              retainedAudioAnalysis.id,
              retainedAudioAnalysis.revision,
              artifactID,
            )
          : "",
    },
    screenshots: {
      view: {
        revision: state.screenshots.revision,
        status: screenshotStatus,
        plan: state.screenshots.value,
        artifacts: workflowView.current?.media
          ? {
              ...workflowView.current.media,
              artifacts: workflowView.current.media.artifacts.map((artifact) => ({
                ...artifact,
                url: workflowMediaURL(artifact.id),
              })),
            }
          : null,
        workflowMode: Boolean(workflowView.current),
        selections: state.screenshots.selections,
        finalSelectionArtifactIDs: selectedScreenshotIDs,
        previewImage: state.screenshots.previewImage,
        staleReason: state.screenshots.staleReason,
        mutationBlockedReason: screenshotMutationBlockedReason,
        error: screenshotError,
      },
      load: loadScreenshotPlan,
      changeSelection: (index, value) =>
        dispatch({ type: "screenshot_selection_changed", index, value }),
      generate: generateScreenshots,
      previewFrame: previewWorkflowFrame,
      remove: (artifactID) => removeMediaArtifacts([artifactID], screenshotCommandCallbacks()),
      removeMany: (artifactIDs) => removeMediaArtifacts(artifactIDs, screenshotCommandCallbacks()),
      selectFinal: (artifactID, selected) => {
        if (!workflowView.current?.media) return Promise.resolve(false);
        return runBackendWorkflow(
          (current, commandID, signal) =>
            activePorts.workflow.setMediaSelection(
              current,
              [artifactID],
              selected,
              commandID,
              signal,
            ),
          screenshotCommandCallbacks(),
        );
      },
      reorderFinal: (fromIndex, toIndex) => {
        const artifactIDs = [...selectedScreenshotIDs];
        if (
          fromIndex === toIndex ||
          fromIndex < 0 ||
          toIndex < 0 ||
          fromIndex >= artifactIDs.length ||
          toIndex >= artifactIDs.length
        ) {
          return Promise.resolve(false);
        }
        const [moved] = artifactIDs.splice(fromIndex, 1);
        artifactIDs.splice(toIndex, 0, moved);
        return persistFinalScreenshotArtifacts(artifactIDs);
      },
      saveFinal: () => persistFinalScreenshotArtifacts(selectedScreenshotIDs),
      selectArtifact: (artifactID, selected) => {
        if (!workflowView.current) return Promise.resolve(false);
        return runBackendWorkflow(
          (current, commandID, signal) =>
            activePorts.workflow.setMediaSelection(
              current,
              [artifactID],
              selected,
              commandID,
              signal,
            ),
          screenshotCommandCallbacks(),
        );
      },
      deleteArtifacts: (artifactIDs) => {
        if (!workflowView.current) return Promise.resolve(false);
        return runBackendWorkflow(
          (current, commandID, signal) =>
            activePorts.workflow.deleteMedia(current, artifactIDs, commandID, signal),
          screenshotCommandCallbacks(),
        );
      },
      readImage: async (artifactID) => workflowMediaURL(artifactID),
    },
    menuImages: {
      view: {
        revision: state.menuImages.revision,
        status: state.menuImages.status,
        images: workflowMenuImages,
        artifacts: workflowView.current?.media || null,
        staleReason: "",
        error: workflowView.failure?.Message || state.menuImages.error,
      },
      load: () => loadMenuImages(),
      importFiles: (files) =>
        runBackendWorkflow(async (current, commandID, signal) => {
          const resources = [];
          for (const file of files) {
            resources.push(await activePorts.workflow.stageMedia(current, file, signal));
          }
          return activePorts.workflow.attachMedia(current, resources, commandID, signal);
        }),
      capture: () =>
        runBackendWorkflow((current, commandID, signal) =>
          continueBackendGoal(
            current,
            "media_ready",
            {
              media: {
                screenshotCount: 0,
                purpose: "menu",
                captureDvdMenus: true,
                maxDvdMenuItems: 0,
              },
            },
            commandID,
            signal,
          ),
        ),
      cancelCapture: () => {
        const controller = controllers.current.workflow;
        if (!controller) return;
        controller.abort();
        delete controllers.current.workflow;
        if (workflowView.current?.operation?.id) {
          void activePorts.workflow.cancelOperation(
            workflowView.current.workflow.id,
            workflowView.current.operation.id,
            new AbortController().signal,
          );
        }
        dispatch({
          type: "workflow_canceled",
          facet: "menuImages",
          sessionRevision: state.sessionRevision,
          revision: state.menuImages.revision,
        });
      },
      remove: (artifactID) => removeMediaArtifacts([artifactID]),
    },
    uploadedImages: {
      view: {
        revision: state.uploadedImages.revision,
        status: state.uploadedImages.status,
        candidates: workflowUploadCandidates,
        uploaded: workflowUploadedImages,
        selectedArtifactIDs: state.uploadedImages.selectedArtifactIDs,
        failures: workflowHostFailures,
        progress: state.uploadedImages.progress,
        staleReason: state.uploadedImages.staleReason,
        error: workflowView.failure?.Message || workflowView.error || state.uploadedImages.error,
      },
      load: () => loadUploadedImages(),
      select: (artifactID, selected) =>
        dispatch({ type: "upload_image_selected", artifactID, selected }),
      selectAll: (selected) => dispatch({ type: "upload_images_selected_all", selected }),
      upload: () => {
        const candidates = new Set(workflowUploadCandidates.map((item) => item.image.artifactID));
        const artifactIDs = state.uploadedImages.selectedArtifactIDs.filter((id) =>
          candidates.has(id),
        );
        return runBackendWorkflow((current, commandID, signal) =>
          activePorts.workflow.uploadImages(current, artifactIDs, commandID, signal),
        );
      },
      remove: (artifactID, _host) =>
        runBackendWorkflow((current, commandID, signal) =>
          activePorts.workflow.removeHostedImages(current, [artifactID], commandID, signal),
        ),
    },
    descriptions: {
      view: {
        revision: state.descriptions.revision,
        status: workflowView.status,
        artifact: workflowView.current?.descriptions || null,
        rawByGroup: state.descriptions.rawByGroup,
        renderedByGroup: state.descriptions.renderedByGroup,
        dirtyGroups: state.descriptions.dirtyGroups,
        staleReason: state.descriptions.staleReason,
        notice: state.descriptions.notice,
        error: workflowView.failure?.Message || state.descriptions.error,
      },
      load: loadDescriptions,
      edit: (groupKey, raw) => dispatch({ type: "description_edited", groupKey, raw }),
      render: renderDescription,
      save: (groupKey) => saveDescription(groupKey, false),
      reset: (groupKey) => saveDescription(groupKey, true),
    },
    upload: {
      view: {
        revision: workflowView.current?.workflow.revision ?? 0,
        selectedTrackers: state.selectedTrackers,
        projections: workflowView.current?.projections || null,
        ignoredDupesFor: state.ignoredDupesFor,
        questionnaireAnswers: state.questionnaireAnswers,
        options: uploadOptions,
        liveTest,
        mutationsAllowed: mutationsAllowed && state.activeInput.state !== "recovering",
        dryRunStatus: workflowDryRunStatus,
        uploadStatus: workflowUploadStatus,
        dryRunResult: workflowView.current?.dryRun || null,
        result: workflowView.current?.uploadResult || null,
        trackerOutcomes: workflowView.current?.continuation?.trackerOutcomes || [],
        submissionExclusions: workflowView.current?.workflow.submissionExclusions || [],
        error: workflowView.failure?.Message || workflowView.error || state.uploadError || "",
      },
      chooseTrackers: (trackers) => dispatch({ type: "trackers_chosen", trackers }),
      answerQuestionnaire: (tracker, key, value) =>
        dispatch({ type: "questionnaire_answered", tracker, key, value }),
      changeOptions: (options: Partial<UploadRunOptions>) =>
        dispatch({
          type: "upload_options_changed",
          value: liveTest ? { ...options, noSeed: true } : options,
        }),
      runDryRun,
      start: async () => {
        if (!mutationsAllowed) return false;
        dispatch({ type: "job_command_started", kind: "upload" });
        if (!workflowView.current) {
          dispatch({
            type: "job_command_failed",
            kind: "upload",
            error: "Prepare the release workflow first.",
          });
          return false;
        }
        const completed = await executeExactUpload();
        if (!completed) {
          dispatch({
            type: "job_command_failed",
            kind: "upload",
            error: workflowView.failure?.Message || "Upload execution failed.",
          });
        }
        return completed;
      },
      cancel: async () => {
        if (!workflowView.current) return false;
        return cancelBackendWorkflow("upload canceled");
      },
      retry: async () => {
        if (!mutationsAllowed) return false;
        const result = workflowView.current?.uploadResult;
        const trackerIDs = (result?.results || [])
          .filter((item) => item.submissionStatus === "failed")
          .map((item) => item.trackerId);
        if (!result || trackerIDs.length === 0) return false;
        return runBackendWorkflow((current, commandID, signal) =>
          activePorts.workflow.retryFailedUploads(
            current,
            { id: result.id, revision: result.revision },
            trackerIDs,
            state.uploadOptions.noSeed,
            commandID,
            signal,
          ),
        );
      },
      retryClientInjection: async () => {
        if (!mutationsAllowed) return false;
        const result = workflowView.current?.uploadResult;
        const trackerIDs = (result?.results || [])
          .filter(
            (item) =>
              item.submissionStatus === "completed" &&
              item.clientInjectionStatus === "failed" &&
              item.clientFailureCode === "client_injection",
          )
          .map((item) => item.trackerId);
        if (!result || trackerIDs.length === 0) return false;
        return runBackendWorkflow((current, commandID, signal) =>
          activePorts.workflow.retryClientInjections(
            current,
            { id: result.id, revision: result.revision },
            trackerIDs,
            commandID,
            signal,
          ),
        );
      },
    },
  };

  return <SessionContext.Provider value={session}>{children}</SessionContext.Provider>;
}

/** Returns the sole active-release workflow interface. */
export const useReleaseSession = (): ReleaseSession => {
  const session = useContext(SessionContext);
  if (!session) throw new Error("ReleaseSessionProvider is required");
  return session;
};

export type { ReleaseSessionPorts } from "./ports";
export type { ReleaseSession } from "./types";
export { routeAccess } from "./navigation";
