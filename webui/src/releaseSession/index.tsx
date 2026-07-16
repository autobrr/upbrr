// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ReactNode } from "react";
import { createContext, useContext, useEffect, useMemo, useReducer, useRef } from "react";
import { useReleaseJobs } from "../jobRegistry";
import type {
  DVDMenuCaptureResult,
  OperationFailure,
  PreparationProgressUpdate,
  ReleaseRef,
  ScreenshotImage,
  ScreenshotPlan,
  ScreenshotPurpose,
  ScreenshotResult,
  ScreenshotSelection,
  UploadImageHostFailure,
} from "../types";
import type { ReleaseSessionPorts, UploadCommand } from "./ports";
import { productionReleaseSessionPorts } from "./production";
import { initialSessionState, sessionReducer } from "./reducer";
import type {
  PreparationIntent,
  PreparationStep,
  ReleaseRoute,
  ReleaseSession,
  RouteAccess,
  UploadRunOptions,
} from "./types";

const emptyRelease: ReleaseRef = { SourcePath: "", Generation: 0 };
const SessionContext = createContext<ReleaseSession | null>(null);

type WorkflowFacet =
  | "screenshots"
  | "menuImages"
  | "uploadedImages"
  | "descriptions"
  | "dryRun"
  | "review";
type ControllerKey = WorkflowFacet | "preparation" | "screenshotRead";
type WorkflowCommand = Readonly<{
  controller: AbortController;
  release: ReleaseRef;
  sessionRevision: number;
  revision: number;
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

const normalizedNames = (values: readonly string[]) =>
  Array.from(new Set(values.map((value) => value.trim().toUpperCase()).filter(Boolean)));

const sameNames = (left: readonly string[], right: readonly string[]) => {
  const normalizedLeft = normalizedNames(left);
  const normalizedRight = normalizedNames(right);
  return (
    normalizedLeft.length === normalizedRight.length &&
    normalizedLeft.every((value, index) => value === normalizedRight[index])
  );
};

const uniqueScreenshotImages = (images: readonly ScreenshotImage[]) => {
  const paths = new Set<string>();
  return images.filter((image) => {
    const path = image.Path.trim();
    if (!path || paths.has(path)) return false;
    paths.add(path);
    return true;
  });
};

const availableScreenshotImages = (plan: ScreenshotPlan | null, result: ScreenshotResult | null) =>
  uniqueScreenshotImages([
    ...(plan?.ExistingScreenshots || []),
    ...(plan?.ExistingTrackerScreenshots || []),
    ...(plan?.FinalSelections || []),
    ...(plan?.PreviewImages || []),
    ...(result?.Images || []),
  ]);

const orderedScreenshotImages = (paths: readonly string[], images: readonly ScreenshotImage[]) => {
  const byPath = new Map(images.map((image) => [image.Path, image]));
  return paths.map((path) => byPath.get(path)).filter((image): image is ScreenshotImage => !!image);
};

const mergeFinalScreenshotImages = (
  current: readonly ScreenshotImage[],
  additions: readonly ScreenshotImage[],
) => {
  const merged = uniqueScreenshotImages(current);
  const indexByPath = new Map(merged.map((image, index) => [image.Path, index]));
  additions.forEach((image) => {
    if (!image.Path) return;
    const existingIndex = indexByPath.get(image.Path);
    if (existingIndex !== undefined) {
      merged[existingIndex] = image;
      return;
    }
    const insertAt = merged.findIndex(
      (entry) => image.TimestampSeconds > 0 && entry.TimestampSeconds > image.TimestampSeconds,
    );
    if (insertAt < 0) merged.push(image);
    else merged.splice(insertAt, 0, image);
    indexByPath.clear();
    merged.forEach((entry, index) => indexByPath.set(entry.Path, index));
  });
  return merged;
};

const isCompleted = (status: string) =>
  ["completed", "completed_with_errors"].includes(status.toLowerCase().trim());

const routeAccess = (
  bound: boolean,
  hasTrackerData: boolean,
  duplicatesReady: boolean,
  descriptionsReady: boolean,
): Readonly<Record<ReleaseRoute, RouteAccess>> => {
  const preparationReason = "Prepare the selected source first.";
  const duplicateReason = duplicatesReady ? "" : "Complete duplicate checking first.";
  return {
    input: { available: true, reason: "" },
    trackerData: {
      available: bound && hasTrackerData,
      reason: bound ? "No tracker data is available." : preparationReason,
    },
    duplicates: {
      available: bound,
      reason: bound ? "" : preparationReason,
    },
    screenshots: {
      available: bound && duplicatesReady,
      reason: bound ? duplicateReason : preparationReason,
    },
    menuImages: {
      available: bound && duplicatesReady,
      reason: bound ? duplicateReason : preparationReason,
    },
    uploadedImages: {
      available: bound && duplicatesReady,
      reason: bound ? duplicateReason : preparationReason,
    },
    descriptions: {
      available: bound && duplicatesReady,
      reason: bound ? duplicateReason : preparationReason,
    },
    upload: {
      available: bound && descriptionsReady,
      reason: bound ? (descriptionsReady ? "" : "Prepare descriptions first.") : preparationReason,
    },
  };
};

const cloneIntent = (intent: PreparationIntent): PreparationIntent => ({
  sourceLookupURL: intent.sourceLookupURL,
  identity: { ...intent.identity },
  releaseName: { ...intent.releaseName },
  playlist: {
    Set: intent.playlist.Set,
    Selected: [...intent.playlist.Selected],
    UseAll: intent.playlist.UseAll,
  },
});

const inferredDiscType = (sourcePath: string) =>
  /(^|[\\/])BDMV([\\/]|$)/i.test(sourcePath) ? "BDMV" : "";

const localPreparationStep = (
  phase: string,
  order: number,
  label: string,
  status: PreparationStep["status"],
  message: string,
): PreparationStep => ({
  phase,
  order,
  label,
  message,
  status,
  timestamp: new Date().toISOString(),
});

const selectedDescriptionGroups = (
  groups: readonly import("../types").DescriptionBuilderGroup[],
  rawByGroup: Readonly<Record<string, string>>,
) =>
  groups.map((group) => ({
    ...group,
    Trackers: [...group.Trackers],
    RawDescription: rawByGroup[group.GroupKey] ?? group.RawDescription,
  }));

/** Owns canonical release workflow state, cancellation, correlation, and transport ports. */
export function ReleaseSessionProvider({
  ports,
  children,
}: Readonly<{ ports?: ReleaseSessionPorts; children: ReactNode }>) {
  const [state, dispatch] = useReducer(sessionReducer, undefined, initialSessionState);
  const controllers = useRef<Partial<Record<ControllerKey, AbortController>>>({});
  const preparationRevision = useRef(0);
  const lastPreparation = useRef<{
    operation: "prepare" | "reset";
    sourcePath: string;
    intent: PreparationIntent;
  } | null>(null);
  const workflowRevisions = useRef<Partial<Record<WorkflowFacet, number>>>({});
  const activePorts = useMemo(() => ports ?? productionReleaseSessionPorts(), [ports]);
  const releaseJobs = useReleaseJobs(state.release ?? emptyRelease);

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
    dispatch({ type: "source_selected", sourcePath: value });
  };

  const dispatchPreparationStep = (
    sourcePath: string,
    commandRevision: number,
    correlationID: string,
    step: PreparationStep,
  ) =>
    dispatch({
      type: "preparation_progressed",
      sourcePath,
      commandRevision,
      correlationID,
      step,
    });

  const executePreparation = async (
    operation: "prepare" | "reset" | "candidate",
    sourcePath: string,
    intent: PreparationIntent,
    controls: Readonly<{ confirmBDMVRescan: boolean }>,
    commandRevision: number,
    correlationID: string,
    controller: AbortController,
    releaseID = "",
  ): Promise<boolean> => {
    try {
      const preview = await activePorts.preparation.execute({
        operation,
        correlationID,
        sourcePath,
        intent,
        controls,
        releaseID,
        signal: controller.signal,
        onProgress: (update: PreparationProgressUpdate) =>
          dispatchPreparationStep(sourcePath, commandRevision, correlationID, {
            phase: update.phase,
            order: update.order,
            label: update.label,
            message: update.message,
            status: update.status,
            timestamp: update.timestamp,
          }),
      });
      dispatch({
        type: "preparation_succeeded",
        sourcePath,
        commandRevision,
        correlationID,
        preview,
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
      dispatch({ type: "source_selected", sourcePath });
    }
    abortController("preparation");
    const controller = new AbortController();
    controllers.current.preparation = controller;
    const commandRevision = Math.max(preparationRevision.current + 1, state.commandRevision + 1);
    preparationRevision.current = commandRevision;
    const correlationID = `preparation-${Date.now().toString(36)}-${commandRevision.toString(36)}`;
    const intent = cloneIntent(requestedIntent);
    lastPreparation.current = { operation, sourcePath, intent };
    dispatch({
      type: "preparation_started",
      sourcePath,
      commandRevision,
      correlationID,
      intent,
    });

    if (intent.playlist.Set) {
      dispatchPreparationStep(
        sourcePath,
        commandRevision,
        correlationID,
        localPreparationStep(
          "playlist_selection",
          40,
          "Accept Blu-ray playlist selection",
          "completed",
          "Using the accepted playlist selection.",
        ),
      );
      return executePreparation(
        operation,
        sourcePath,
        intent,
        controls,
        commandRevision,
        correlationID,
        controller,
      );
    }

    dispatchPreparationStep(
      sourcePath,
      commandRevision,
      correlationID,
      localPreparationStep(
        "disc_detection",
        10,
        "Detect disc type",
        "running",
        "Inspecting source.",
      ),
    );
    let discType = "";
    try {
      discType = (await activePorts.preparation.detectDiscType(sourcePath, controller.signal))
        .trim()
        .toUpperCase();
      dispatchPreparationStep(
        sourcePath,
        commandRevision,
        correlationID,
        localPreparationStep(
          "disc_detection",
          10,
          "Detect disc type",
          "completed",
          "Source inspected.",
        ),
      );
    } catch {
      if (controller.signal.aborted) {
        if (controllers.current.preparation === controller) delete controllers.current.preparation;
        return false;
      }
      discType = inferredDiscType(sourcePath);
      dispatchPreparationStep(
        sourcePath,
        commandRevision,
        correlationID,
        localPreparationStep(
          "disc_detection",
          10,
          "Detect disc type",
          "completed",
          "Source inspected using the path hint.",
        ),
      );
    }

    if (controller.signal.aborted) {
      if (controllers.current.preparation === controller) delete controllers.current.preparation;
      return false;
    }

    if (discType !== "BDMV") {
      dispatchPreparationStep(
        sourcePath,
        commandRevision,
        correlationID,
        localPreparationStep(
          "playlist_discovery",
          20,
          "Discover Blu-ray playlists",
          "skipped",
          "Source is not a Blu-ray disc.",
        ),
      );
      return executePreparation(
        operation,
        sourcePath,
        intent,
        controls,
        commandRevision,
        correlationID,
        controller,
      );
    }

    dispatchPreparationStep(
      sourcePath,
      commandRevision,
      correlationID,
      localPreparationStep(
        "playlist_discovery",
        20,
        "Discover Blu-ray playlists",
        "running",
        "Discovering playlists.",
      ),
    );
    try {
      const discovered = await activePorts.preparation.discoverPlaylists(
        sourcePath,
        controller.signal,
      );
      if (controller.signal.aborted) return false;
      const candidates = [...discovered]
        .sort((left, right) => (right.score || 0) - (left.score || 0))
        .slice(0, 10);
      dispatchPreparationStep(
        sourcePath,
        commandRevision,
        correlationID,
        localPreparationStep(
          "playlist_discovery",
          20,
          "Discover Blu-ray playlists",
          "completed",
          `${candidates.length} playlist${candidates.length === 1 ? "" : "s"} found.`,
        ),
      );
      dispatchPreparationStep(
        sourcePath,
        commandRevision,
        correlationID,
        localPreparationStep(
          "playlist_selection",
          30,
          "Select Blu-ray playlists",
          candidates.length ? "awaiting_input" : "failed",
          candidates.length ? "Select one or more playlists." : "No Blu-ray playlists were found.",
        ),
      );
      dispatch({
        type: "playlist_required",
        sourcePath,
        commandRevision,
        correlationID,
        candidates,
        error: candidates.length ? "" : "No BDMV playlists were found.",
      });
    } catch (error) {
      if (!controller.signal.aborted) {
        dispatchPreparationStep(
          sourcePath,
          commandRevision,
          correlationID,
          localPreparationStep(
            "playlist_discovery",
            20,
            "Discover Blu-ray playlists",
            "failed",
            "Playlist discovery failed.",
          ),
        );
        dispatch({
          type: "playlist_required",
          sourcePath,
          commandRevision,
          correlationID,
          candidates: [],
          error: errorText(error),
        });
      }
    } finally {
      if (controllers.current.preparation === controller) delete controllers.current.preparation;
    }
    return false;
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
    dispatch({
      type: "preparation_started",
      sourcePath,
      commandRevision,
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
    if (!command) return false;
    try {
      const plan = await activePorts.screenshots.load(command.release, command.controller.signal);
      dispatch({
        type: "screenshots_loaded",
        sessionRevision: command.sessionRevision,
        revision: command.revision,
        plan,
        reseedDrafts: true,
      });
      return !command.controller.signal.aborted;
    } catch (error) {
      failWorkflow("screenshots", command, error);
      return false;
    } finally {
      finishWorkflow("screenshots", command);
    }
  };

  const mutateScreenshots = async (
    mutate: (command: WorkflowCommand) => Promise<ScreenshotResult | null>,
  ): Promise<boolean> => {
    const command = beginWorkflow("screenshots", access.screenshots.reason);
    if (!command) return false;
    try {
      const result = await mutate(command);
      const plan = await activePorts.screenshots.load(command.release, command.controller.signal);
      dispatch({
        type: "screenshots_loaded",
        sessionRevision: command.sessionRevision,
        revision: command.revision,
        plan,
        ...(result ? { result } : {}),
        changed: true,
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
    const command = beginWorkflow("screenshots", access.screenshots.reason);
    if (!command) return false;
    try {
      const requested = [...(selections ?? state.screenshots.selections)];
      const existingIndices = new Set(
        [
          ...(state.screenshots.value?.ExistingScreenshots || []),
          ...(state.screenshots.value?.FinalSelections || []),
          ...(state.screenshots.result?.Purpose === "final"
            ? state.screenshots.result.Images || []
            : []),
        ].map((image) => image.Index),
      );
      const captureSelections =
        purpose === "final"
          ? requested.filter((selection) => !existingIndices.has(selection.Index))
          : requested;
      if (captureSelections.length === 0) {
        throw new Error(
          purpose === "final"
            ? "All requested screenshots already exist."
            : "No screenshot selections available.",
        );
      }
      const result = await activePorts.screenshots.generate(
        command.release,
        captureSelections,
        purpose,
        command.controller.signal,
      );
      let finalSelectionPaths: readonly string[] | undefined;
      if (purpose === "final") {
        const available = availableScreenshotImages(
          state.screenshots.value,
          state.screenshots.result,
        );
        const current = orderedScreenshotImages(state.screenshots.finalSelectionPaths, available);
        const finalImages = mergeFinalScreenshotImages(current, result.Images || []);
        await activePorts.screenshots.saveFinal(
          command.release,
          finalImages,
          command.controller.signal,
        );
        finalSelectionPaths = finalImages.map((image) => image.Path);
      }
      const plan = await activePorts.screenshots.load(command.release, command.controller.signal);
      dispatch({
        type: "screenshots_loaded",
        sessionRevision: command.sessionRevision,
        revision: command.revision,
        plan,
        result,
        changed: true,
        ...(finalSelectionPaths ? { finalSelectionPaths } : {}),
      });
      return !command.controller.signal.aborted;
    } catch (error) {
      failWorkflow("screenshots", command, error);
      return false;
    } finally {
      finishWorkflow("screenshots", command);
    }
  };

  const persistFinalScreenshotPaths = async (imagePaths: readonly string[]) => {
    const normalizedPaths = Array.from(
      new Set(imagePaths.map((path) => path.trim()).filter(Boolean)),
    );
    const images = orderedScreenshotImages(
      normalizedPaths,
      availableScreenshotImages(state.screenshots.value, state.screenshots.result),
    );
    dispatch({ type: "screenshot_final_paths_changed", imagePaths: normalizedPaths });
    return mutateScreenshots(async (command) => {
      await activePorts.screenshots.saveFinal(command.release, images, command.controller.signal);
      return null;
    });
  };

  const removeScreenshots = async (imagePaths: readonly string[]) => {
    const paths = Array.from(new Set(imagePaths.map((path) => path.trim()).filter(Boolean)));
    if (paths.length === 0) return false;
    const deletedPaths = new Set(paths);
    const linkedURLs = (state.screenshots.value?.TrackerImageLinks || [])
      .filter((link) => deletedPaths.has(link.Path))
      .map((link) => link.URL)
      .filter(Boolean);
    const remainingFinalPaths = state.screenshots.finalSelectionPaths.filter(
      (path) => !deletedPaths.has(path),
    );
    const remainingFinalImages = orderedScreenshotImages(
      remainingFinalPaths,
      availableScreenshotImages(state.screenshots.value, state.screenshots.result),
    );
    dispatch({ type: "screenshot_final_paths_changed", imagePaths: remainingFinalPaths });
    return mutateScreenshots(async (command) => {
      for (const path of paths) {
        await activePorts.screenshots.remove(command.release, path, command.controller.signal);
      }
      for (const url of linkedURLs) {
        await activePorts.screenshots.removeTrackerURL(
          command.release,
          url,
          command.controller.signal,
        );
      }
      if (remainingFinalPaths.length !== state.screenshots.finalSelectionPaths.length) {
        await activePorts.screenshots.saveFinal(
          command.release,
          remainingFinalImages,
          command.controller.signal,
        );
      }
      return null;
    });
  };

  const removeTrackerURLs = async (urls: readonly string[]) => {
    const normalizedURLs = Array.from(new Set(urls.map((url) => url.trim()).filter(Boolean)));
    if (normalizedURLs.length === 0) return false;
    return mutateScreenshots(async (command) => {
      for (const url of normalizedURLs) {
        await activePorts.screenshots.removeTrackerURL(
          command.release,
          url,
          command.controller.signal,
        );
      }
      return null;
    });
  };

  const loadMenuImages = async (
    mutate?: (command: WorkflowCommand) => Promise<DVDMenuCaptureResult | null>,
  ): Promise<boolean> => {
    const command = beginWorkflow("menuImages", access.menuImages.reason);
    if (!command) return false;
    try {
      const capture = mutate ? await mutate(command) : null;
      const images = await activePorts.menuImages.list(command.release, command.controller.signal);
      const previews = await Promise.all(
        images.map(async (image) => ({
          image,
          dataURI: image.Path
            ? await activePorts.menuImages.readImage(image.Path, command.controller.signal)
            : "",
        })),
      );
      dispatch({
        type: "menu_images_loaded",
        sessionRevision: command.sessionRevision,
        revision: command.revision,
        images: previews,
        ...(capture ? { capture } : {}),
        changed: Boolean(mutate),
      });
      return !command.controller.signal.aborted;
    } catch (error) {
      failWorkflow("menuImages", command, error);
      return false;
    } finally {
      finishWorkflow("menuImages", command);
    }
  };

  const loadUploadedImages = async (
    mutate?: (
      command: WorkflowCommand,
    ) => Promise<Readonly<{ failures: readonly UploadImageHostFailure[] }>>,
    correlationID = "",
  ): Promise<boolean> => {
    const command = beginWorkflow("uploadedImages", access.uploadedImages.reason);
    if (!command) return false;
    dispatch({
      type: "uploaded_images_progress_reset",
      sessionRevision: command.sessionRevision,
      revision: command.revision,
      correlationID,
    });
    try {
      const mutation = mutate
        ? await mutate(command)
        : { failures: [] as readonly UploadImageHostFailure[] };
      const [candidateImages, uploaded] = await Promise.all([
        activePorts.uploadedImages.listCandidates(command.release, command.controller.signal),
        activePorts.uploadedImages.listUploaded(command.release, command.controller.signal),
      ]);
      const candidates = await Promise.all(
        candidateImages.map(async (image) => ({
          image,
          dataURI: image.Path
            ? await activePorts.uploadedImages.readImage(image.Path, command.controller.signal)
            : "",
        })),
      );
      dispatch({
        type: "uploaded_images_loaded",
        sessionRevision: command.sessionRevision,
        revision: command.revision,
        candidates,
        uploaded,
        failures: mutation.failures,
        changed: Boolean(mutate),
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
    const command = beginWorkflow("descriptions", access.descriptions.reason);
    if (!command) return false;
    try {
      const preview = await activePorts.descriptions.load(
        command.release,
        state.selectedTrackers,
        command.controller.signal,
      );
      dispatch({
        type: "descriptions_loaded",
        sessionRevision: command.sessionRevision,
        revision: command.revision,
        preview,
      });
      return !command.controller.signal.aborted;
    } catch (error) {
      failWorkflow("descriptions", command, error);
      return false;
    } finally {
      finishWorkflow("descriptions", command);
    }
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
    const command = beginWorkflow("descriptions", access.descriptions.reason);
    if (!command) return false;
    const key = groupKey.trim();
    const group = state.descriptions.value?.Groups.find((item) => item.GroupKey === key);
    const inputRevision = state.descriptions.inputRevision;
    if (!group) {
      failWorkflow("descriptions", command, new Error("Description group not found."));
      finishWorkflow("descriptions", command);
      return false;
    }
    try {
      const updated = await activePorts.descriptions.save(
        command.release,
        key,
        reset ? "" : state.descriptions.rawByGroup[key] || "",
        group.Trackers,
        command.controller.signal,
      );
      dispatch({
        type: "description_saved",
        sessionRevision: command.sessionRevision,
        revision: command.revision,
        inputRevision,
        group: updated,
      });
      return !command.controller.signal.aborted;
    } catch (error) {
      failWorkflow("descriptions", command, error);
      return false;
    } finally {
      finishWorkflow("descriptions", command);
    }
  };

  const buildUploadCommand = (release: ReleaseRef): UploadCommand => ({
    release,
    trackers: [...state.selectedTrackers],
    ignoreDupesFor: [...state.ignoredDupesFor],
    questionnaireAnswers: Object.fromEntries(
      Object.entries(state.questionnaireAnswers).map(([tracker, answers]) => [
        tracker,
        { ...answers },
      ]),
    ),
    descriptionGroups: selectedDescriptionGroups(
      state.descriptions.value?.Groups || [],
      state.descriptions.rawByGroup,
    ),
    options: { ...state.uploadOptions },
  });

  const runDryRun = async (): Promise<boolean> => {
    const command = beginWorkflow("dryRun", access.upload.reason);
    if (!command) return false;
    const inputRevision = state.uploadInputRevision;
    try {
      const preview = await activePorts.upload.dryRun(
        buildUploadCommand(command.release),
        command.controller.signal,
      );
      dispatch({
        type: "dry_run_loaded",
        sessionRevision: command.sessionRevision,
        revision: command.revision,
        inputRevision,
        preview,
      });
      return !command.controller.signal.aborted;
    } catch (error) {
      failWorkflow("dryRun", command, error);
      return false;
    } finally {
      finishWorkflow("dryRun", command);
    }
  };

  const reviewUpload = async (): Promise<boolean> => {
    const staleReason =
      state.dryRun.status === "ready" && !state.dryRun.staleReason
        ? access.upload.reason
        : state.dryRun.staleReason || "Run the current dry run first.";
    const command = beginWorkflow("review", staleReason);
    if (!command) return false;
    const inputRevision = state.uploadInputRevision;
    try {
      const review = await activePorts.upload.review(
        buildUploadCommand(command.release),
        command.controller.signal,
      );
      dispatch({
        type: "review_loaded",
        sessionRevision: command.sessionRevision,
        revision: command.revision,
        inputRevision,
        review,
      });
      return !command.controller.signal.aborted;
    } catch (error) {
      failWorkflow("review", command, error);
      return false;
    } finally {
      finishWorkflow("review", command);
    }
  };

  const currentDupeSnapshot = releaseJobs.dupe?.dupe ?? null;
  const assessedDupeSelection =
    currentDupeSnapshot?.summary?.Eligibility?.Trackers?.map((tracker) => tracker.Tracker) || [];
  const runningDupeSelection =
    currentDupeSnapshot?.trackers?.map((tracker) => tracker.tracker) || [];
  const dupeSelection = assessedDupeSelection.length ? assessedDupeSelection : runningDupeSelection;
  const duplicateAssessmentCurrent = sameNames(dupeSelection, state.selectedTrackers);
  const duplicateStartPending = releaseJobs.pending.some(
    (pending) => pending.kind === "duplicate_check",
  );
  const duplicatesReady =
    !duplicateStartPending &&
    duplicateAssessmentCurrent &&
    isCompleted(String(currentDupeSnapshot?.status || ""));
  const uploadSnapshot = releaseJobs.upload?.upload ?? null;
  const bound = Boolean(state.release) && !state.preparationDirty;
  const access = routeAccess(
    bound,
    Boolean(state.preview?.TrackerData?.length),
    duplicatesReady,
    state.descriptions.status === "ready" && !state.descriptions.staleReason,
  );

  const session: ReleaseSession = {
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
        error: state.preparation.error,
        failure: state.preparation.failure,
        preparationDirty: state.preparationDirty,
        intent: state.preparationIntent,
        selectedTrackers: state.selectedTrackers,
        progress: {
          correlationID: state.preparation.correlationID,
          status: state.preparation.status,
          message: state.preparation.message,
          steps: state.preparation.steps,
        },
        preview: state.preview,
        trackerData: state.preview?.TrackerData || [],
        playlist: state.playlist,
      },
      updateSourceDraft: (value) => dispatch({ type: "draft_changed", value }),
      selectSource,
      changeSourceLookupURL: (value) => dispatch({ type: "source_lookup_changed", value }),
      changeIdentity: (value) => dispatch({ type: "identity_changed", value }),
      changeReleaseName: (value) => dispatch({ type: "release_name_changed", value }),
      chooseTrackers: (trackers) => dispatch({ type: "trackers_chosen", trackers }),
      choosePlaylists: (playlists, useAll) =>
        dispatch({ type: "playlist_draft_changed", playlists, useAll }),
      confirmPlaylists: () => {
        if (
          !state.playlist.required ||
          state.playlist.selected.length === 0 ||
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
        dispatchPreparationStep(
          sourcePath,
          commandRevision,
          correlationID,
          localPreparationStep(
            "playlist_selection",
            30,
            "Select Blu-ray playlists",
            "completed",
            "Playlist selection accepted.",
          ),
        );
        return executePreparation(
          operation,
          sourcePath,
          intent,
          { confirmBDMVRescan: false },
          commandRevision,
          correlationID,
          controller,
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
        status: duplicateStartPending
          ? "running"
          : currentDupeSnapshot
            ? duplicatesReady
              ? "ready"
              : isCompleted(currentDupeSnapshot.status)
                ? "idle"
                : "running"
            : "idle",
        snapshot: !duplicateStartPending && duplicateAssessmentCurrent ? currentDupeSnapshot : null,
        eligibility:
          !duplicateStartPending && duplicateAssessmentCurrent
            ? currentDupeSnapshot?.summary?.Eligibility || null
            : null,
        ignoredTrackers: state.ignoredDupesFor,
        selectedTrackers: state.selectedTrackers,
        error: state.duplicatesError || currentDupeSnapshot?.failure?.Message || "",
        transientError: releaseJobs.transientError,
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
        try {
          await releaseJobs.startDupe({ trackers: [...state.selectedTrackers] });
          return true;
        } catch (error) {
          dispatch({ type: "job_command_failed", kind: "duplicates", error: errorText(error) });
          return false;
        }
      },
      chooseTrackers: (trackers) => dispatch({ type: "trackers_chosen", trackers }),
      cancel: async () => {
        if (!releaseJobs.dupe) return false;
        try {
          await releaseJobs.cancel(releaseJobs.dupe);
          return true;
        } catch (error) {
          dispatch({ type: "job_command_failed", kind: "duplicates", error: errorText(error) });
          return false;
        }
      },
      setIgnored: (tracker, ignored) => dispatch({ type: "dupe_ignore_changed", tracker, ignored }),
    },
    screenshots: {
      view: {
        revision: state.screenshots.revision,
        status: state.screenshots.status,
        plan: state.screenshots.value,
        result: state.screenshots.result,
        selections: state.screenshots.selections,
        finalSelectionPaths: state.screenshots.finalSelectionPaths,
        previewImage: state.screenshots.previewImage,
        staleReason: state.screenshots.staleReason,
        error: state.screenshots.error,
      },
      load: loadScreenshotPlan,
      changeSelection: (index, value) =>
        dispatch({ type: "screenshot_selection_changed", index, value }),
      generate: generateScreenshots,
      previewFrame: async (timestampSeconds) => {
        const command = beginWorkflow("screenshots", access.screenshots.reason);
        if (!command) return false;
        try {
          const image = await activePorts.screenshots.previewFrame(
            command.release,
            timestampSeconds,
            command.controller.signal,
          );
          dispatch({
            type: "screenshot_previewed",
            sessionRevision: command.sessionRevision,
            revision: command.revision,
            image,
          });
          return !command.controller.signal.aborted;
        } catch (error) {
          failWorkflow("screenshots", command, error);
          return false;
        } finally {
          finishWorkflow("screenshots", command);
        }
      },
      remove: (imagePath) => removeScreenshots([imagePath]),
      removeMany: removeScreenshots,
      removeTrackerURL: (url) => removeTrackerURLs([url]),
      removeTrackerURLs,
      selectFinal: (imagePath, selected) => {
        const paths = new Set(state.screenshots.finalSelectionPaths);
        if (selected) paths.add(imagePath);
        else paths.delete(imagePath);
        return persistFinalScreenshotPaths([...paths]);
      },
      reorderFinal: (fromIndex, toIndex) => {
        const paths = [...state.screenshots.finalSelectionPaths];
        if (
          fromIndex === toIndex ||
          fromIndex < 0 ||
          toIndex < 0 ||
          fromIndex >= paths.length ||
          toIndex >= paths.length
        ) {
          return Promise.resolve(false);
        }
        const [moved] = paths.splice(fromIndex, 1);
        paths.splice(toIndex, 0, moved);
        return persistFinalScreenshotPaths(paths);
      },
      saveFinal: () => persistFinalScreenshotPaths(state.screenshots.finalSelectionPaths),
      readImage: async (path) => {
        if (controllers.current.screenshotRead)
          throw new Error("An image read is already running.");
        const controller = new AbortController();
        controllers.current.screenshotRead = controller;
        try {
          return await activePorts.screenshots.readImage(path, controller.signal);
        } finally {
          if (controllers.current.screenshotRead === controller) {
            delete controllers.current.screenshotRead;
          }
        }
      },
    },
    menuImages: {
      view: {
        revision: state.menuImages.revision,
        status: state.menuImages.status,
        images: state.menuImages.value || [],
        capture: state.menuImages.capture,
        staleReason: state.menuImages.staleReason,
        error: state.menuImages.error,
      },
      load: () => loadMenuImages(),
      importPaths: (paths) =>
        loadMenuImages(async (command) => {
          await activePorts.menuImages.importPaths(
            command.release,
            paths,
            command.controller.signal,
          );
          return null;
        }),
      capture: () =>
        loadMenuImages((command) =>
          activePorts.menuImages.capture(command.release, command.controller.signal),
        ),
      cancelCapture: () => {
        const controller = controllers.current.menuImages;
        if (!controller) return;
        controller.abort();
        delete controllers.current.menuImages;
        dispatch({
          type: "workflow_canceled",
          facet: "menuImages",
          sessionRevision: state.sessionRevision,
          revision: state.menuImages.revision,
        });
      },
      remove: (imagePath) =>
        loadMenuImages(async (command) => {
          await activePorts.menuImages.remove(
            command.release,
            imagePath,
            command.controller.signal,
          );
          return null;
        }),
    },
    uploadedImages: {
      view: {
        revision: state.uploadedImages.revision,
        status: state.uploadedImages.status,
        candidates: state.uploadedImages.value?.candidates || [],
        uploaded: state.uploadedImages.value?.uploaded || [],
        selectedPaths: state.uploadedImages.selectedPaths,
        host: state.uploadedImages.host,
        failures: state.uploadedImages.failures,
        progress: state.uploadedImages.progress,
        staleReason: state.uploadedImages.staleReason,
        error: state.uploadedImages.error,
      },
      load: () => loadUploadedImages(),
      chooseHost: (host) => dispatch({ type: "upload_host_changed", host }),
      select: (imagePath, selected) =>
        dispatch({ type: "upload_image_selected", imagePath, selected }),
      selectAll: (selected) => dispatch({ type: "upload_images_selected_all", selected }),
      upload: () => {
        const selectedPaths = new Set(state.uploadedImages.selectedPaths);
        const images = (state.uploadedImages.value?.candidates || [])
          .filter((item) => selectedPaths.has(item.image.Path))
          .map((item) => item.image);
        const correlationID = `image-upload-${Date.now().toString(36)}-${(
          state.uploadedImages.revision + 1
        ).toString(36)}`;
        return loadUploadedImages(async (command) => {
          const result = await activePorts.uploadedImages.upload({
            correlationID,
            release: command.release,
            trackers: state.selectedTrackers,
            host: state.uploadedImages.host,
            images,
            signal: command.controller.signal,
            onProgress: (update) =>
              dispatch({
                type: "uploaded_images_progressed",
                sessionRevision: command.sessionRevision,
                revision: command.revision,
                update,
              }),
          });
          return {
            failures: result.Failures || [],
          };
        }, correlationID);
      },
      remove: (imagePath, host) =>
        loadUploadedImages(async (command) => {
          await activePorts.uploadedImages.remove(
            command.release,
            imagePath,
            host,
            command.controller.signal,
          );
          return { failures: [] };
        }),
    },
    descriptions: {
      view: {
        revision: state.descriptions.revision,
        status: state.descriptions.status,
        preview: state.descriptions.value,
        rawByGroup: state.descriptions.rawByGroup,
        renderedByGroup: state.descriptions.renderedByGroup,
        dirtyGroups: state.descriptions.dirtyGroups,
        staleReason: state.descriptions.staleReason,
        notice: state.descriptions.notice,
        error: state.descriptions.error,
      },
      load: loadDescriptions,
      edit: (groupKey, raw) => dispatch({ type: "description_edited", groupKey, raw }),
      render: renderDescription,
      save: (groupKey) => saveDescription(groupKey, false),
      reset: (groupKey) => saveDescription(groupKey, true),
    },
    upload: {
      view: {
        revision: Math.max(state.dryRun.revision, state.review.revision),
        selectedTrackers: state.selectedTrackers,
        eligibility: duplicateAssessmentCurrent
          ? currentDupeSnapshot?.summary?.Eligibility || null
          : null,
        ignoredDupesFor: state.ignoredDupesFor,
        questionnaireAnswers: state.questionnaireAnswers,
        options: state.uploadOptions,
        dryRunStatus: state.dryRun.status,
        dryRun: state.dryRun.value,
        dryRunStaleReason: state.dryRun.staleReason,
        reviewStatus: state.review.status,
        review: state.review.value,
        reviewStaleReason: state.review.staleReason,
        snapshot: uploadSnapshot,
        error: state.uploadError || uploadSnapshot?.failure?.Message || "",
        transientError: releaseJobs.transientError,
      },
      chooseTrackers: (trackers) => dispatch({ type: "trackers_chosen", trackers }),
      answerQuestionnaire: (tracker, key, value) =>
        dispatch({ type: "questionnaire_answered", tracker, key, value }),
      changeOptions: (options: Partial<UploadRunOptions>) =>
        dispatch({ type: "upload_options_changed", value: options }),
      runDryRun,
      review: reviewUpload,
      start: async () => {
        dispatch({ type: "job_command_started", kind: "upload" });
        const review = state.review;
        if (
          !state.release ||
          review.status !== "ready" ||
          review.staleReason ||
          !review.value?.Token
        ) {
          dispatch({
            type: "job_command_failed",
            kind: "upload",
            error: review.staleReason || "Complete the current upload review first.",
          });
          return false;
        }
        try {
          await releaseJobs.startUpload(review.value.Token);
          return true;
        } catch (error) {
          dispatch({ type: "job_command_failed", kind: "upload", error: errorText(error) });
          return false;
        }
      },
      cancel: async () => {
        if (!releaseJobs.upload) return false;
        try {
          await releaseJobs.cancel(releaseJobs.upload);
          return true;
        } catch (error) {
          dispatch({ type: "job_command_failed", kind: "upload", error: errorText(error) });
          return false;
        }
      },
      retry: async () => {
        if (!releaseJobs.upload) {
          dispatch({
            type: "job_command_failed",
            kind: "upload",
            error: "No retained upload job is available.",
          });
          return false;
        }
        try {
          await releaseJobs.retryUpload(releaseJobs.upload);
          return true;
        } catch (error) {
          dispatch({ type: "job_command_failed", kind: "upload", error: errorText(error) });
          return false;
        }
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
