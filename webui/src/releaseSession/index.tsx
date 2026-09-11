// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ReactNode } from "react";
import { createContext, useContext, useEffect, useMemo, useReducer, useRef, useState } from "react";
import type {
  ApplicationInfo,
  MetadataPreview,
  OperationFailure,
  PrepareInput,
  ReleaseRef,
  ScreenshotPlan,
  ScreenshotPurpose,
  ScreenshotSelection,
  UploadImageHostFailure,
} from "../types";
import type {
  DescriptionInstructions,
  ContinueReleaseWorkflowRequest,
  DupeDecision,
  MediaCaptureInstructions,
  Operation as WorkflowOperationStatus,
  PrepareInput as WorkflowPrepareInput,
  RequiredAction,
  ReleaseFactInstructions,
  ReleaseCorrectionPatch,
  ReleaseWorkflowCurrent,
  WorkflowContinuation,
  WorkflowGoal,
  WorkflowIntent,
} from "../api/generated/release-workflow";
import type { ReleaseSessionPorts } from "./ports";
import { productionReleaseSessionPorts } from "./production";
import { correctionValuesFor, initialSessionState, sessionReducer } from "./reducer";
import type {
  PreparationIntent,
  ReleaseRoute,
  ReleaseSession,
  RouteAccess,
  UploadRunOptions,
} from "./types";

const SessionContext = createContext<ReleaseSession | null>(null);

type WorkflowFacet = "screenshots" | "menuImages" | "uploadedImages" | "descriptions";
type ControllerKey = WorkflowFacet | "preparation" | "workflow";
type WorkflowCommand = Readonly<{
  controller: AbortController;
  release: ReleaseRef;
  sessionRevision: number;
  revision: number;
}>;

type PendingInputUpdate = Readonly<{
  inputEditRevision: number;
  correctionDirty: boolean;
  resetFields: NonNullable<ReleaseCorrectionPatch["resetFields"]>;
  confirmFields: NonNullable<ReleaseCorrectionPatch["confirmFields"]>;
  valueFields: NonNullable<ReleaseCorrectionPatch["resetFields"]>;
  trackerInputAnswers: NonNullable<WorkflowIntent["trackerInputAnswers"]>;
  selectedTrackers: readonly string[];
}>;

const errorText = (error: unknown) =>
  error instanceof Error && error.message ? error.message : String(error);

const workflowViewValue = <T,>(value: unknown): T => structuredClone(value) as T;

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

const normalizedNames = (values: readonly string[]) =>
  Array.from(new Set(values.map((value) => value.trim().toUpperCase()).filter(Boolean)));

const playlistSelectionComplete = (
  candidates: readonly { id: string; discId: string }[],
  selected: readonly string[],
) => {
  const selectedIDs = new Set(selected);
  const selectedDiscs = new Set(
    candidates
      .filter((candidate) => selectedIDs.has(candidate.id))
      .map((candidate) => candidate.discId.trim() || "single-disc"),
  );
  return (
    candidates.length > 0 &&
    new Set(candidates.map((candidate) => candidate.discId.trim() || "single-disc")).size ===
      selectedDiscs.size
  );
};

const workflowFactInstructions = (
  instructions: PrepareInput["Instructions"],
): ReleaseFactInstructions => ({
  Identity: instructions.Identity,
  ...(instructions.Category !== undefined ? { Category: instructions.Category } : {}),
  ReleaseName: instructions.ReleaseName,
  Metadata: instructions.Metadata ?? {},
  SourceLookup: instructions.SourceLookup,
  BlurayReleaseID: instructions.BlurayReleaseID ?? "",
  Playlist: instructions.Playlist,
  TrackerIDs: instructions.TrackerIDs ?? {},
});

const workflowPrepareInput = (input: PrepareInput): WorkflowPrepareInput => ({
  SourcePath: input.SourcePath,
  Intent: input.Intent,
  Instructions: workflowFactInstructions(input.Instructions),
  Policy: {
    KeepFolder: input.Policy.KeepFolder,
    KeepImages: input.Policy.KeepImages ?? false,
    OnlyID: input.Policy.OnlyID,
  },
  Search: {
    Skip: input.Search?.Skip ?? false,
    ...(input.Search?.Client !== undefined ? { Client: input.Search.Client } : {}),
  },
  Controls: {
    Interaction: input.Controls?.Interaction ?? "",
    ConfirmBDMVRescan: input.Controls?.ConfirmBDMVRescan ?? false,
    ...(input.Controls?.ForceRecheck !== undefined
      ? { ForceRecheck: input.Controls.ForceRecheck }
      : {}),
  },
  Force: input.Force,
  RequirePrepared: false,
});

const workflowDescriptionScreenshotCount = (current: ReleaseWorkflowCurrent) =>
  current.media?.artifacts.filter((artifact) => artifact.selected && artifact.kind === "screenshot")
    .length || 0;

const workflowDescriptionImageHostOverrides = (
  failedHosts: readonly string[],
): DescriptionInstructions["imageHost"] => {
  const FailedHosts = Array.from(
    new Set(failedHosts.map((host) => host.trim().toLowerCase()).filter(Boolean)),
  );
  return { FailedHosts, SkipUpload: true };
};

const sameNames = (left: readonly string[], right: readonly string[]) => {
  const normalizedLeft = normalizedNames(left);
  const normalizedRight = normalizedNames(right);
  return (
    normalizedLeft.length === normalizedRight.length &&
    normalizedLeft.every((value, index) => value === normalizedRight[index])
  );
};

const isActiveWorkflowOperation = (
  operation: WorkflowOperationStatus | null | undefined,
): operation is WorkflowOperationStatus =>
  operation?.status === "queued" || operation?.status === "running";

const isFailedWorkflowOperation = (operation: WorkflowOperationStatus) =>
  ["failed", "interrupted", "canceled"].includes(operation.status);

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

type TrackerWorkflowRequirements = Readonly<{
  needsImages: boolean;
  needsDescriptions: boolean;
}>;

export const routeAccess = (
  continuation: WorkflowContinuation | null | undefined,
  hasTrackerData: boolean,
  requirements: TrackerWorkflowRequirements,
): Readonly<Record<ReleaseRoute, RouteAccess>> => {
  const goal = (name: string): RouteAccess => {
    const availability = continuation?.availableGoals.find((candidate) => candidate.goal === name);
    return {
      available: availability?.available === true,
      reason:
        availability?.available === true
          ? ""
          : availability?.reason || "Workflow state is not ready yet.",
    };
  };
  const trackerAssessment = goal("trackers_assessed");
  const media = goal("media_ready");
  const descriptions = goal("descriptions_ready");
  const upload = goal("upload_reviewed");
  return {
    input: { available: true, reason: "" },
    trackerData: {
      available: trackerAssessment.available && hasTrackerData,
      reason: trackerAssessment.available
        ? "No tracker data is available."
        : trackerAssessment.reason,
    },
    duplicates: trackerAssessment,
    screenshots: {
      available: media.available && requirements.needsImages,
      reason: requirements.needsImages
        ? media.reason
        : "Selected trackers do not use shared screenshots.",
    },
    menuImages: media,
    uploadedImages: {
      available: media.available && requirements.needsImages,
      reason: requirements.needsImages
        ? media.reason
        : "Selected trackers do not use shared screenshots.",
    },
    descriptions: {
      available: descriptions.available && requirements.needsDescriptions,
      reason: requirements.needsDescriptions
        ? descriptions.reason
        : "Selected trackers do not use shared descriptions.",
    },
    upload,
  };
};

const cloneIntent = (intent: PreparationIntent): PreparationIntent => ({
  sourceLookupURL: intent.sourceLookupURL,
  identity: { ...intent.identity },
  metadata: {
    ...intent.metadata,
    ...(intent.metadata.Genres ? { Genres: [...intent.metadata.Genres] } : {}),
    ...(intent.metadata.AudioLanguages
      ? { AudioLanguages: [...intent.metadata.AudioLanguages] }
      : {}),
    ...(intent.metadata.SubtitleLanguages
      ? { SubtitleLanguages: [...intent.metadata.SubtitleLanguages] }
      : {}),
    ...(intent.metadata.HardcodedSubtitleLanguages
      ? { HardcodedSubtitleLanguages: [...intent.metadata.HardcodedSubtitleLanguages] }
      : {}),
    ...(intent.metadata.TrackLanguages
      ? {
          TrackLanguages: intent.metadata.TrackLanguages.map((correction) => ({
            ...correction,
            languages: [...correction.languages],
          })),
        }
      : {}),
  },
  releaseName: { ...intent.releaseName },
  playlist: {
    Set: intent.playlist.Set,
    Selected: [...intent.playlist.Selected],
    UseAll: intent.playlist.UseAll,
  },
  trackerSourceIDs: { ...intent.trackerSourceIDs },
  policy: { ...intent.policy },
  search: { ...intent.search },
});

const preparationWithoutFactCorrections = (input: WorkflowPrepareInput): WorkflowPrepareInput => ({
  ...input,
  Instructions: {
    ...input.Instructions,
    Identity: {},
    ReleaseName: {},
    Metadata: {},
    ...(input.Instructions.Category !== undefined ? { Category: undefined } : {}),
  },
});

const correctionPatchFor = (
  current: ReleaseWorkflowCurrent,
  intent: PreparationIntent,
  update: PendingInputUpdate,
): ReleaseCorrectionPatch => ({
  values: correctionValuesFor(intent, update.valueFields),
  resetFields: update.resetFields.map((field) => ({ ...field })),
  confirmFields: update.confirmFields.map((field) => ({ ...field })),
  expectedRevision:
    current.corrections?.revision ?? current.factInstructions?.correctionRevision ?? 0,
});

// The API serializes automatic corrections as null; local drafts omit those keys.
const manualCorrections = <T extends object>(values: T): T => {
  const corrections = { ...values };
  for (const key in corrections) {
    if (corrections[key] === null || corrections[key] === undefined) delete corrections[key];
  }
  return corrections;
};

const workflowPreparationIntent = (
  current: ReleaseWorkflowCurrent,
  submitted?: PreparationIntent,
): PreparationIntent => {
  const instructions = current.factInstructions?.instructions;
  const corrections = current.corrections?.corrections;
  return {
    sourceLookupURL: instructions?.SourceLookup || "",
    identity: manualCorrections(corrections?.identity || instructions?.Identity || {}),
    metadata: manualCorrections(corrections?.metadata || instructions?.Metadata || {}),
    releaseName: manualCorrections(corrections?.releaseName || instructions?.ReleaseName || {}),
    playlist: {
      Set: Boolean(instructions?.Playlist?.Set),
      Selected: [...(instructions?.Playlist?.Selected || [])],
      UseAll: Boolean(instructions?.Playlist?.UseAll),
    },
    trackerSourceIDs: { ...(instructions?.TrackerIDs || {}) },
    policy: submitted
      ? { ...submitted.policy }
      : { keepFolder: false, keepImages: false, onlyID: false },
    search: submitted ? { ...submitted.search } : { skip: false, client: "" },
  };
};

const preparationWithEffectiveCorrections = (
  preparation: WorkflowPrepareInput,
  facts: ReleaseFactInstructions,
): WorkflowPrepareInput => ({
  ...preparation,
  Instructions: {
    ...preparation.Instructions,
    Identity: facts.Identity,
    ReleaseName: facts.ReleaseName,
    Metadata: facts.Metadata,
    ...(facts.Category !== undefined ? { Category: facts.Category } : { Category: undefined }),
  },
});

const workflowSelectedInputTrackers = (current: ReleaseWorkflowCurrent) =>
  current.inputReadiness?.selectedTrackerIds ?? current.selection?.trackerIds;

const metadataPreviewFromWorkflow = (current: ReleaseWorkflowCurrent): MetadataPreview | null => {
  const snapshot = current.release;
  if (!snapshot) return null;
  const release = snapshot.release;
  const trackerData = [...(snapshot.display.TrackerData || [])];
  return {
    SourcePath: release.Source.SourcePath,
    TrackerName: trackerData[0]?.Tracker || "",
    ReleaseName: snapshot.display.ReleaseName || release.Naming.ReleaseName,
    ReleaseNameOverrides: { ...(current.factInstructions?.instructions.ReleaseName || {}) },
    Release: {
      SourcePath: release.Source.SourcePath,
      Generation: release.Generation,
    },
    Identity: release.Identity,
    Display: workflowViewValue<MetadataPreview["Display"]>(snapshot.display),
    Bluray: workflowViewValue<MetadataPreview["Bluray"]>(release.ProviderMetadata.Bluray || null),
    Diagnostics: workflowViewValue<MetadataPreview["Diagnostics"]>(snapshot.diagnostics || []),
    TrackerData: workflowViewValue<MetadataPreview["TrackerData"]>(trackerData),
  };
};

const workflowStorageKey = "upbrr.activeReleaseWorkflow";

const storedWorkflowID = () => {
  try {
    return window.sessionStorage.getItem(workflowStorageKey)?.trim() || "";
  } catch {
    return "";
  }
};

const storeWorkflowID = (workflowID: string) => {
  try {
    if (workflowID) window.sessionStorage.setItem(workflowStorageKey, workflowID);
    else window.sessionStorage.removeItem(workflowStorageKey);
  } catch {
    // Storage can be unavailable in hardened/private browser contexts.
  }
};

const preparationInputForWorkflow = (
  sourcePath: string,
  intent: PreparationIntent,
  confirmBDMVRescan: boolean,
): PrepareInput => ({
  SourcePath: sourcePath,
  Intent: "preview",
  Instructions: {
    Identity: { ...intent.identity },
    Category: intent.releaseName.Category,
    ReleaseName: { ...intent.releaseName },
    Metadata: { ...intent.metadata },
    SourceLookup: intent.sourceLookupURL,
    BlurayReleaseID: "",
    Playlist: {
      Set: intent.playlist.Set,
      Selected: [...intent.playlist.Selected],
      UseAll: intent.playlist.UseAll,
    },
    TrackerIDs: Object.fromEntries(
      Object.entries(intent.trackerSourceIDs).filter(([, value]) => value !== ""),
    ),
  },
  Policy: {
    KeepFolder: intent.policy.keepFolder,
    KeepImages: intent.policy.keepImages,
    OnlyID: intent.policy.onlyID,
  },
  Search: {
    Skip: intent.search.skip,
    ...(intent.search.client.trim() ? { Client: intent.search.client.trim() } : {}),
  },
  Controls: {
    Interaction: "interactive",
    ConfirmBDMVRescan: confirmBDMVRescan,
  },
  Force: false,
});

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
  const liveTest = testRuntime?.mode === "live_test";
  const mutationsAllowed = runtimeInfoReady && !liveTest;
  const uploadOptions = { ...state.uploadOptions, noSeed: liveTest || state.uploadOptions.noSeed };
  const normalizedDefaultTrackers = useMemo(
    () => normalizedNames(defaultTrackers),
    [defaultTrackers],
  );
  const controllers = useRef<Partial<Record<ControllerKey, AbortController>>>({});
  const preparationRevision = useRef(0);
  const lastPreparation = useRef<{
    operation: "prepare" | "reset";
    sourcePath: string;
    intent: PreparationIntent;
  } | null>(null);
  const workflowRevisions = useRef<Partial<Record<WorkflowFacet, number>>>({});
  const lastWorkflowError = useRef<unknown>(null);
  const activePorts = useMemo(() => ports ?? productionReleaseSessionPorts(), [ports]);
  const [workflowView, setWorkflowView] = useState<{
    status: "idle" | "running" | "ready" | "error";
    current: ReleaseWorkflowCurrent | null;
    error: string;
    failure: OperationFailure | null;
  }>({ status: "idle", current: null, error: "", failure: null });

  useEffect(() => {
    dispatch({
      type: "default_trackers_received",
      sessionRevision: state.sessionRevision,
      trackers: normalizedDefaultTrackers,
    });
  }, [normalizedDefaultTrackers, state.sessionRevision]);

  const publishWorkflowCurrent = (current: ReleaseWorkflowCurrent, status: "running" | "ready") => {
    storeWorkflowID(current.workflow.id);
    const selectedTrackers = workflowSelectedInputTrackers(current);
    if (selectedTrackers) {
      dispatch({ type: "trackers_received", trackers: selectedTrackers });
    }
    setWorkflowView({ status, current, error: "", failure: null });
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
  ): Promise<ReleaseWorkflowCurrent> => {
    publishWorkflowCurrent(initial, "running");
    let operation = initial.operation;
    if (!isActiveWorkflowOperation(operation)) return initial;

    while (operation && isActiveWorkflowOperation(operation)) {
      await waitForWorkflowPoll(signal);
      operation = await activePorts.workflow.operation(initial.workflow.id, operation.id, signal);
      const update = operation;
      setWorkflowView((view) => ({
        ...view,
        status: isActiveWorkflowOperation(update) ? "running" : view.status,
        current: view.current ? { ...view.current, operation: update } : view.current,
      }));
    }

    const current = await activePorts.workflow.current(initial.workflow.id, signal);
    publishWorkflowCurrent(current, "running");
    const terminalOperation = operation as WorkflowOperationStatus | undefined;
    if (terminalOperation && isFailedWorkflowOperation(terminalOperation)) {
      const failure = terminalOperation.failures?.[0]?.failure;
      if (failure) throw workflowOperationFailureError(failure);
      throw new Error(
        terminalOperation.message || `Workflow operation ${terminalOperation.status}.`,
      );
    }
    return current;
  };

  const failBackendWorkflow = (error: unknown) => {
    const failure = operationFailureFromError(error);
    if (failure?.Code === "missing_prerequisite" && failure.Recovery === "refresh_release") {
      storeWorkflowID("");
    }
    setWorkflowView((current) => ({
      ...current,
      status: "error",
      error: errorText(error),
      failure,
    }));
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
      const next = await awaitWorkflowCommand(
        await activePorts.workflow.continue(
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
        ),
        signal,
      );
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

  const reloadBackendWorkflow = async (): Promise<boolean> => {
    const workflowID = workflowView.current?.workflow.id || storedWorkflowID();
    if (!workflowID) return false;
    abortController("workflow");
    const controller = new AbortController();
    controllers.current.workflow = controller;
    setWorkflowView((current) => ({ ...current, status: "running", error: "", failure: null }));
    try {
      const current = await awaitWorkflowCommand(
        await activePorts.workflow.current(workflowID, controller.signal),
        controller.signal,
      );
      releaseWorkflowController(controller);
      if (controller.signal.aborted) return false;
      acceptWorkflowCurrent(current);
      const sourcePath = current.release?.release.Source.SourcePath || "";
      if (sourcePath) {
        const commandRevision = current.workflow.revision;
        const correlationID = `workflow-restore-${current.workflow.id}-${commandRevision}`;
        const intent = workflowPreparationIntent(current);
        preparationRevision.current = Math.max(preparationRevision.current, commandRevision);
        lastPreparation.current = { operation: "prepare", sourcePath, intent };
        dispatch({
          type: "source_selected",
          sourcePath,
          defaultTrackers: normalizedDefaultTrackers,
        });
        const selectedTrackers = workflowSelectedInputTrackers(current);
        if (selectedTrackers) {
          dispatch({ type: "trackers_received", trackers: selectedTrackers });
        }
        dispatch({
          type: "preparation_started",
          sourcePath,
          commandRevision,
          inputEditRevision: 0,
          correlationID,
          intent,
        });
        if (!dispatchPlaylistAction(current, sourcePath, commandRevision, correlationID)) {
          const preview = metadataPreviewFromWorkflow(current);
          if (!preview) throw new Error("Workflow release snapshot is unavailable.");
          dispatch({
            type: "preparation_succeeded",
            sourcePath,
            commandRevision,
            correlationID,
            preview,
            intent,
            selectedTrackers,
          });
        }
      }
      return true;
    } catch (error) {
      releaseWorkflowController(controller);
      if (!controller.signal.aborted) failBackendWorkflow(error);
      return false;
    } finally {
      releaseWorkflowController(controller);
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
  ): Promise<ReleaseWorkflowCurrent | null> => {
    if (controllers.current.workflow) return null;
    const controller = new AbortController();
    controllers.current.workflow = controller;
    setWorkflowView((current) => ({ ...current, status: "running", error: "", failure: null }));
    const commandID = `workflow-${Date.now().toString(36)}-${state.commandRevision.toString(36)}`;
    lastWorkflowError.current = null;
    try {
      const intent: WorkflowIntent = {
        factInstructions: workflowFactInstructions(input.Instructions),
        preparation: workflowPrepareInput(input),
        trackerIds: [...update.selectedTrackers],
      };
      const created = await awaitWorkflowCommand(
        await activePorts.workflow.continue(
          {
            goal: "input_ready",
            intent,
            idempotencyKey: commandID,
          },
          controller.signal,
        ),
        controller.signal,
      );
      const prepared = await continueBackendGoal(
        created,
        "input_ready",
        intent,
        commandID,
        controller.signal,
      );
      releaseWorkflowController(controller);
      if (controller.signal.aborted) return null;
      return prepared;
    } catch (error) {
      releaseWorkflowController(controller);
      lastWorkflowError.current = error;
      if (!controller.signal.aborted) failBackendWorkflow(error);
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
  ): Promise<boolean> => {
    if (!workflowView.current || controllers.current.workflow) return false;
    const controller = new AbortController();
    controllers.current.workflow = controller;
    setWorkflowView((current) => ({ ...current, status: "running", error: "", failure: null }));
    const commandID = `workflow-${Date.now().toString(36)}-${workflowView.current.workflow.revision.toString(36)}`;
    try {
      const current = await awaitWorkflowCommand(
        await execute(workflowView.current, commandID, controller.signal),
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

  const cancelBackendWorkflow = async (reason: string): Promise<boolean> => {
    const workflowID = workflowView.current?.workflow.id || storedWorkflowID();
    if (!workflowID) return false;
    const operation = workflowView.current?.operation;
    abortController("workflow");
    const controller = new AbortController();
    controllers.current.workflow = controller;
    setWorkflowView((current) => ({ ...current, status: "running", error: "", failure: null }));
    const commandID = `workflow-cancel-${Date.now().toString(36)}`;
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
    setWorkflowView((current) => ({ ...current, status: "running", error: "", failure: null }));
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

  useEffect(() => {
    if (!storedWorkflowID()) return;
    void reloadBackendWorkflow();
    // Reload once when the transport binding changes; commands own later refreshes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activePorts.workflow]);

  const abortController = (key: ControllerKey) => {
    controllers.current[key]?.abort();
    delete controllers.current[key];
  };

  const abortAll = () => {
    Object.values(controllers.current).forEach((controller) => controller?.abort());
    controllers.current = {};
  };

  useEffect(() => abortAll, []);

  const selectSource = (value: string) => {
    abortAll();
    dispatch({
      type: "source_selected",
      sourcePath: value,
      defaultTrackers: normalizedDefaultTrackers,
    });
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
        setWorkflowView((view) => ({
          ...view,
          status: "running",
          error: "",
          failure: null,
        }));
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
        setWorkflowView((view) => ({
          ...view,
          status: "running",
          error: "",
          failure: null,
        }));
        const commandID = `workflow-reset-${Date.now().toString(36)}-${previous.workflow.revision.toString(36)}`;
        const resetInput = workflowPrepareInput({ ...input, Force: true });
        current = await continueBackendGoal(
          previous,
          "input_ready",
          {
            ...(update.correctionDirty
              ? { correctionPatch: correctionPatchFor(previous, intent, update) }
              : {}),
            preparation: preparationWithoutFactCorrections(resetInput),
            trackerIds: [...update.selectedTrackers],
          },
          commandID,
          controller.signal,
        );
      } else if (intent.playlist.Set && pendingPlaylist && workflowView.current) {
        setWorkflowView((view) => ({
          ...view,
          status: "running",
          error: "",
          failure: null,
        }));
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
        const previous = workflowView.current;
        if (previous?.release?.release.Source.SourcePath === sourcePath) {
          const preparedInput = workflowPrepareInput(input);
          const commandID = `workflow-input-${Date.now().toString(36)}-${previous.workflow.revision.toString(36)}`;
          current = await continueBackendGoal(
            previous,
            "input_ready",
            {
              ...(update.correctionDirty
                ? { correctionPatch: correctionPatchFor(previous, intent, update) }
                : {}),
              preparation: preparationWithoutFactCorrections(preparedInput),
              trackerIds: [...update.selectedTrackers],
            },
            commandID,
            controller.signal,
          );
        } else {
          current = await startBackendWorkflow(input, update);
        }
      }
      if (!current) {
        throw (
          lastWorkflowError.current || new Error("Canonical workflow preparation did not complete.")
        );
      }
      acceptWorkflowCurrent(current);
      if (dispatchPlaylistAction(current, sourcePath, commandRevision, correlationID)) {
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
        acceptWorkflowCurrent(current);
      }
      const preview = metadataPreviewFromWorkflow(current);
      if (!preview) throw new Error("Workflow release snapshot is unavailable.");
      dispatch({
        type: "preparation_succeeded",
        sourcePath,
        commandRevision,
        correlationID,
        preview,
        intent: workflowPreparationIntent(current, intent),
        trackerInputsAccepted,
        selectedTrackers: workflowSelectedInputTrackers(current),
      });
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
    const sourcePath = requestedSource.trim();
    if (!sourcePath) return false;
    if (sourcePath !== state.selectedSource) {
      abortAll();
      dispatch({
        type: "source_selected",
        sourcePath,
        defaultTrackers: normalizedDefaultTrackers,
      });
    }
    abortController("preparation");
    abortController("workflow");
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

  const runPreparation = (operation: "prepare" | "reset") =>
    runPreparationFor(operation, state.selectedSource, state.preparationIntent);

  const selectCandidate = async (releaseID: string): Promise<boolean> => {
    const sourcePath = state.selectedSource.trim();
    const candidateID = releaseID.trim();
    if (!sourcePath || !candidateID) return false;
    abortController("preparation");
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
      dispatch({
        type: "workflow_failed",
        facet,
        sessionRevision: command.sessionRevision,
        revision: command.revision,
        error: errorText(error),
      });
    }
  };

  const finishWorkflow = (facet: WorkflowFacet, command: WorkflowCommand) => {
    if (controllers.current[facet] === command.controller) delete controllers.current[facet];
  };

  const loadScreenshotPlan = async (): Promise<boolean> => {
    const command = beginWorkflow("screenshots", access.screenshots.reason);
    if (!command || !workflowView.current) return false;
    try {
      const workflowPlan = await activePorts.workflow.mediaPlan(
        workflowView.current.workflow.id,
        command.controller.signal,
      );
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
      return runBackendWorkflow((current, commandID, signal) =>
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
    return runBackendWorkflow((current, commandID, signal) =>
      activePorts.workflow.reorderMedia(current, normalizedArtifactIDs, commandID, signal),
    );
  };

  const removeMediaArtifacts = async (artifactIDs: readonly string[]) => {
    const normalizedArtifactIDs = Array.from(
      new Set(artifactIDs.map((artifactID) => artifactID.trim()).filter(Boolean)),
    );
    if (normalizedArtifactIDs.length === 0) return false;
    if (!workflowView.current?.media) return false;
    return runBackendWorkflow((current, commandID, signal) =>
      activePorts.workflow.deleteMedia(current, normalizedArtifactIDs, commandID, signal),
    );
  };

  const previewWorkflowFrame = async (
    discID: string,
    timestampSeconds: number,
  ): Promise<boolean> => {
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

  const loadDescriptions = async (): Promise<boolean> => {
    if (!workflowView.current?.media) return false;
    if (workflowView.current.descriptions) return true;
    const completed = await runBackendWorkflow((current, commandID, signal) =>
      continueBackendGoal(
        current,
        "descriptions_ready",
        {
          descriptions: {
            questionnaireAnswers: state.questionnaireAnswers,
            options: {
              RunLogLevel: state.uploadOptions.runLogLevel,
              Screens: workflowDescriptionScreenshotCount(current),
              NoSeed: uploadOptions.noSeed,
              SkipAutoTorrent: false,
              OnlyID: false,
              KeepFolder: false,
              KeepImages: false,
              CaptureDVDMenus: false,
              InteractionMode: "interactive",
            },
            imageHost: workflowDescriptionImageHostOverrides(current.media?.failedHosts || []),
            templateVersion: "workflow-v1",
          },
        },
        `${commandID}-descriptions`,
        signal,
      ),
    );
    if (completed) dispatch({ type: "description_dirty_cleared" });
    return completed;
  };

  const renderDescription = async (groupKey: string): Promise<boolean> => {
    const command = beginWorkflow("descriptions", access.descriptions.reason);
    if (!command) return false;
    const key = groupKey.trim();
    const inputRevision = state.descriptions.inputRevision;
    try {
      const html = await activePorts.descriptions.render(
        state.descriptions.rawByGroup[key] || "",
        command.controller.signal,
      );
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
            state.descriptions.rawByGroup[key] || "",
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
  const backendResolvedUploadIntent = () => ({ noSeed: uploadOptions.noSeed });

  const runDryRun = async (): Promise<boolean> => {
    if (!workflowView.current) return false;
    return runBackendWorkflow((current, commandID, signal) =>
      continueBackendGoal(current, "dry_run", backendResolvedUploadIntent(), commandID, signal),
    );
  };

  const executeExactUpload = async (): Promise<boolean> => {
    if (!mutationsAllowed) return false;
    if (!workflowView.current || controllers.current.workflow) return false;
    const controller = new AbortController();
    controllers.current.workflow = controller;
    setWorkflowView((current) => ({ ...current, status: "running", error: "", failure: null }));
    const commandID = `workflow-upload-${Date.now().toString(36)}-${workflowView.current.workflow.revision.toString(36)}`;
    try {
      let current = workflowView.current;
      if (!current.dryRun) {
        current = await continueBackendGoal(
          current,
          "dry_run",
          backendResolvedUploadIntent(),
          `${commandID}-review`,
          controller.signal,
        );
      }
      if (!current.dryRun) {
        throw new Error("Exact upload dry run is unavailable.");
      }
      const uploaded = await continueBackendGoal(
        current,
        "uploaded",
        backendResolvedUploadIntent(),
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
  const access = routeAccess(
    workflowView.current?.continuation,
    Boolean(state.preview?.TrackerData?.length),
    requirements,
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
        : "idle";
  const trackerInputAnswers = state.trackerInputAnswers;

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
          continueBackendGoal(current, "dry_run", backendResolvedUploadIntent(), commandID, signal),
        ),
      executeUploads: () => executeExactUpload(),
      confirmAction: (action: RequiredAction, confirmed = true) => {
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
            backendResolvedUploadIntent(),
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
        const sourcePath = state.selectedSource;
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
      prepareSource: (sourcePath, intent) => runPreparationFor("prepare", sourcePath, intent),
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
    screenshots: {
      view: {
        revision: state.screenshots.revision,
        status: state.screenshots.status === "running" ? "running" : workflowView.status,
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
        error: workflowView.failure?.Message || workflowView.error || state.screenshots.error,
      },
      load: loadScreenshotPlan,
      changeSelection: (index, value) =>
        dispatch({ type: "screenshot_selection_changed", index, value }),
      generate: generateScreenshots,
      previewFrame: previewWorkflowFrame,
      remove: (artifactID) => removeMediaArtifacts([artifactID]),
      removeMany: removeMediaArtifacts,
      selectFinal: (artifactID, selected) => {
        if (!workflowView.current?.media) return Promise.resolve(false);
        return runBackendWorkflow((current, commandID, signal) =>
          activePorts.workflow.setMediaSelection(
            current,
            [artifactID],
            selected,
            commandID,
            signal,
          ),
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
        return runBackendWorkflow((current, commandID, signal) =>
          activePorts.workflow.setMediaSelection(
            current,
            [artifactID],
            selected,
            commandID,
            signal,
          ),
        );
      },
      deleteArtifacts: (artifactIDs) => {
        if (!workflowView.current) return Promise.resolve(false);
        return runBackendWorkflow((current, commandID, signal) =>
          activePorts.workflow.deleteMedia(current, artifactIDs, commandID, signal),
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
        mutationsAllowed,
        dryRunStatus: workflowDryRunStatus,
        uploadStatus: workflowUploadStatus,
        dryRunResult: workflowView.current?.dryRun || null,
        result: workflowView.current?.uploadResult || null,
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
