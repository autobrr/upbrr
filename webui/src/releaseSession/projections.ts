// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { MetadataPreview, PrepareInput } from "../types";
import type {
  DescriptionInstructions,
  Operation as WorkflowOperationStatus,
  ReleaseFactInstructions,
  ReleaseCorrectionPatch,
  ReleaseWorkflowCurrent,
  PrepareInput as WorkflowPrepareInput,
  WorkflowIntent,
} from "../api/generated/release-workflow";
import type { PreparationIntent } from "./types";
import { correctionValuesFor } from "./reducer";

export type PendingInputUpdate = Readonly<{
  inputEditRevision: number;
  correctionDirty: boolean;
  resetFields: NonNullable<ReleaseCorrectionPatch["resetFields"]>;
  confirmFields: NonNullable<ReleaseCorrectionPatch["confirmFields"]>;
  valueFields: NonNullable<ReleaseCorrectionPatch["resetFields"]>;
  trackerInputAnswers: NonNullable<WorkflowIntent["trackerInputAnswers"]>;
  selectedTrackers: readonly string[];
}>;

export const workflowViewValue = <T>(value: unknown): T => structuredClone(value) as T;

export const normalizedNames = (values: readonly string[]) =>
  Array.from(new Set(values.map((value) => value.trim().toUpperCase()).filter(Boolean)));

export const playlistSelectionComplete = (
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

export const workflowFactInstructions = (
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

export const workflowPrepareInput = (input: PrepareInput): WorkflowPrepareInput => ({
  SourcePath: input.SourcePath,
  Intent: input.Intent,
  ExternalFreshness: "refresh",
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

export const workflowDescriptionScreenshotCount = (current: ReleaseWorkflowCurrent) =>
  current.media?.artifacts.filter((artifact) => artifact.selected && artifact.kind === "screenshot")
    .length || 0;

export const workflowDescriptionImageHostOverrides = (
  failedHosts: readonly string[],
): DescriptionInstructions["imageHost"] => {
  const FailedHosts = Array.from(
    new Set(failedHosts.map((host) => host.trim().toLowerCase()).filter(Boolean)),
  );
  return { FailedHosts, SkipUpload: true };
};

export const sameNames = (left: readonly string[], right: readonly string[]) => {
  const normalizedLeft = normalizedNames(left);
  const normalizedRight = normalizedNames(right);
  return (
    normalizedLeft.length === normalizedRight.length &&
    normalizedLeft.every((value, index) => value === normalizedRight[index])
  );
};

export const isActiveWorkflowOperation = (
  operation: WorkflowOperationStatus | null | undefined,
): operation is WorkflowOperationStatus =>
  operation?.status === "queued" || operation?.status === "running";

export const isFailedWorkflowOperation = (operation: WorkflowOperationStatus) =>
  ["failed", "interrupted", "canceled"].includes(operation.status);

export const cloneIntent = (intent: PreparationIntent): PreparationIntent => ({
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

export const emptyPreparationIntent = (): PreparationIntent => ({
  sourceLookupURL: "",
  identity: {},
  metadata: {},
  releaseName: {},
  playlist: { Set: false, Selected: [], UseAll: false },
  trackerSourceIDs: {},
  policy: { keepFolder: false, keepImages: false, onlyID: false },
  search: { skip: false, client: "" },
});

export const preparationWithoutFactCorrections = (
  input: WorkflowPrepareInput,
): WorkflowPrepareInput => ({
  ...input,
  Instructions: {
    ...input.Instructions,
    Identity: {},
    ReleaseName: {},
    Metadata: {},
    ...(input.Instructions.Category !== undefined ? { Category: undefined } : {}),
  },
});

export const correctionPatchFor = (
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

export const workflowPreparationIntent = (
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

export const preparationWithEffectiveCorrections = (
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

export const workflowSelectedInputTrackers = (current: ReleaseWorkflowCurrent) =>
  current.inputReadiness?.selectedTrackerIds ?? current.selection?.trackerIds;

export const metadataPreviewFromWorkflow = (
  current: ReleaseWorkflowCurrent,
): MetadataPreview | null => {
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

export const preparationInputForWorkflow = (
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

export const preparationIntentFromInput = (input: PrepareInput): PreparationIntent => ({
  sourceLookupURL: input.Instructions.SourceLookup,
  identity: { ...input.Instructions.Identity },
  metadata: { ...(input.Instructions.Metadata || {}) },
  releaseName: { ...input.Instructions.ReleaseName },
  playlist: {
    Set: Boolean(input.Instructions.Playlist?.Set),
    Selected: [...(input.Instructions.Playlist?.Selected || [])],
    UseAll: Boolean(input.Instructions.Playlist?.UseAll),
  },
  trackerSourceIDs: { ...(input.Instructions.TrackerIDs || {}) },
  policy: {
    keepFolder: input.Policy.KeepFolder,
    keepImages: input.Policy.KeepImages ?? false,
    onlyID: input.Policy.OnlyID,
  },
  search: {
    skip: input.Search?.Skip ?? false,
    client: input.Search?.Client || "",
  },
});
