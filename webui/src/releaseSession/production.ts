// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { descriptionClient, releaseWorkflowClient } from "../api/app";
import type { ReleaseSessionPorts } from "./ports";

/** Composes production transports once at the application boundary. */
export const productionReleaseSessionPorts = (): ReleaseSessionPorts => ({
  workflow: {
    continue: (request, signal) => releaseWorkflowClient.continue(request, signal),
    current: (workflowID, signal) => releaseWorkflowClient.current(workflowID, signal),
    operation: (workflowID, operationID, signal) =>
      releaseWorkflowClient.operation(workflowID, operationID, signal),
    cancelOperation: (workflowID, operationID, signal) =>
      releaseWorkflowClient.cancelOperation(workflowID, operationID, signal),
    mediaPlan: (workflowID, signal) => releaseWorkflowClient.mediaPlan(workflowID, signal),
    previewFrame: (current, timestampSeconds, idempotencyKey, signal) =>
      releaseWorkflowClient.previewFrame(
        {
          workflowId: current.workflow.id,
          expectedRevision: current.workflow.revision,
          timestampSeconds,
          idempotencyKey,
        },
        signal,
      ),
    setMediaSelection: (current, artifactIDs, selected, idempotencyKey, signal) => {
      if (!current.media) throw new Error("Workflow media is unavailable.");
      return releaseWorkflowClient.setMediaSelection(
        {
          workflowId: current.workflow.id,
          expectedRevision: current.workflow.revision,
          media: { id: current.media.id, revision: current.media.revision },
          artifactIds: artifactIDs,
          selected,
          idempotencyKey,
        },
        signal,
      );
    },
    deleteMedia: (current, artifactIDs, idempotencyKey, signal) => {
      if (!current.media) throw new Error("Workflow media is unavailable.");
      return releaseWorkflowClient.deleteMedia(
        {
          workflowId: current.workflow.id,
          expectedRevision: current.workflow.revision,
          media: { id: current.media.id, revision: current.media.revision },
          artifactIds: artifactIDs,
          idempotencyKey,
        },
        signal,
      );
    },
    reorderMedia: (current, artifactIDs, idempotencyKey, signal) => {
      if (!current.media) throw new Error("Workflow media is unavailable.");
      return releaseWorkflowClient.reorderMedia(
        {
          workflowId: current.workflow.id,
          expectedRevision: current.workflow.revision,
          media: { id: current.media.id, revision: current.media.revision },
          artifactIds: artifactIDs,
          idempotencyKey,
        },
        signal,
      );
    },
    stageMedia: (current, file, signal) =>
      releaseWorkflowClient.stageMedia(
        current.workflow.id,
        current.workflow.revision,
        file,
        signal,
      ),
    attachMedia: (current, resources, idempotencyKey, signal) =>
      releaseWorkflowClient.attachMedia(
        {
          workflowId: current.workflow.id,
          expectedRevision: current.workflow.revision,
          ...(current.media
            ? { media: { id: current.media.id, revision: current.media.revision } }
            : {}),
          attachments: resources.map((resource, order) => ({
            resource,
            kind: "dvd_menu",
            purpose: "menu",
            order,
          })),
          idempotencyKey,
        },
        signal,
      ),
    uploadImages: (current, artifactIDs, idempotencyKey, signal) => {
      if (!current.media) throw new Error("Workflow media is unavailable.");
      return releaseWorkflowClient.uploadImages(
        {
          workflowId: current.workflow.id,
          expectedRevision: current.workflow.revision,
          media: { id: current.media.id, revision: current.media.revision },
          artifactIds: artifactIDs,
          idempotencyKey,
        },
        signal,
      );
    },
    retryImageHost: (current, artifactIDs, host, idempotencyKey, signal) => {
      if (!current.media) throw new Error("Workflow media is unavailable.");
      return releaseWorkflowClient.retryImageHost(
        {
          workflowId: current.workflow.id,
          expectedRevision: current.workflow.revision,
          media: { id: current.media.id, revision: current.media.revision },
          artifactIds: artifactIDs,
          host,
          idempotencyKey,
        },
        signal,
      );
    },
    removeHostedImages: (current, artifactIDs, idempotencyKey, signal) => {
      if (!current.media) throw new Error("Workflow media is unavailable.");
      return releaseWorkflowClient.removeHostedImages(
        {
          workflowId: current.workflow.id,
          expectedRevision: current.workflow.revision,
          media: { id: current.media.id, revision: current.media.revision },
          artifactIds: artifactIDs,
          idempotencyKey,
        },
        signal,
      );
    },
    mediaURL: (current, artifactID) => {
      if (!current.media) return "";
      return releaseWorkflowClient.mediaURL(
        current.workflow.id,
        current.media.id,
        current.media.revision,
        artifactID,
      );
    },
    saveDescriptionOverride: (current, groupKey, source, idempotencyKey, signal) => {
      if (!current.descriptions) throw new Error("Workflow descriptions are unavailable.");
      return releaseWorkflowClient.saveDescriptionOverride(
        {
          workflowId: current.workflow.id,
          expectedRevision: current.workflow.revision,
          idempotencyKey,
          override: {
            descriptions: {
              id: current.descriptions.id,
              revision: current.descriptions.revision,
            },
            groupKey,
            source,
          },
        },
        signal,
      );
    },
    resetDescriptionOverride: (current, groupKey, idempotencyKey, signal) => {
      if (!current.descriptions) throw new Error("Workflow descriptions are unavailable.");
      return releaseWorkflowClient.resetDescriptionOverride(
        {
          workflowId: current.workflow.id,
          expectedRevision: current.workflow.revision,
          idempotencyKey,
          descriptions: {
            id: current.descriptions.id,
            revision: current.descriptions.revision,
          },
          groupKey,
        },
        signal,
      );
    },
    retryFailedUploads: (current, result, trackerIDs, noSeed, idempotencyKey, signal) =>
      releaseWorkflowClient.retryFailedUploads(
        {
          workflowId: current.workflow.id,
          expectedRevision: current.workflow.revision,
          retry: { result, trackerIds: [...trackerIDs] },
          noSeed,
          idempotencyKey,
        },
        signal,
      ),
    retryClientInjections: (current, result, trackerIDs, idempotencyKey, signal) =>
      releaseWorkflowClient.retryClientInjections(
        {
          workflowId: current.workflow.id,
          expectedRevision: current.workflow.revision,
          retry: { result, trackerIds: [...trackerIDs] },
          idempotencyKey,
        },
        signal,
      ),
    cancel: async (workflowID, reason, idempotencyKey, signal) => {
      const current = await releaseWorkflowClient.current(workflowID, signal);
      return releaseWorkflowClient.cancel(
        {
          workflowId: workflowID,
          expectedRevision: current.workflow.revision,
          reason,
          idempotencyKey,
        },
        signal,
      );
    },
    invalidateTrackers: (current, trackerIDs, reason, idempotencyKey, signal) =>
      releaseWorkflowClient.invalidateTrackers(
        {
          workflowId: current.workflow.id,
          expectedRevision: current.workflow.revision,
          trackerIds: [...trackerIDs],
          reason,
          idempotencyKey,
        },
        signal,
      ),
  },
  descriptions: {
    render: (raw, signal) => descriptionClient.render(raw, signal),
  },
});
