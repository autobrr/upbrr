// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import {
  descriptionClient,
  menuImageClient,
  playlistClient,
  preparationClient,
  screenshotClient,
  uploadedImageClient,
  uploadClient,
} from "../api/app";
import { subscribeWebEvent } from "../api/client";
import type { ImageUploadProgressUpdate, PreparationProgressUpdate } from "../types";
import type { ReleaseSessionPorts } from "./ports";

const preparationProgressEvent = "preparation:progress";
const imageUploadProgressEvent = "image-upload:progress";

const executePreparation: ReleaseSessionPorts["preparation"]["execute"] = async (command) => {
  const off = subscribeWebEvent(preparationProgressEvent, (payload: unknown) => {
    const update = payload as PreparationProgressUpdate;
    if (update?.correlationID === command.correlationID) command.onProgress(update);
  });
  try {
    switch (command.operation) {
      case "prepare":
        return await preparationClient.fetchMetadata(
          command.correlationID,
          command.sourcePath,
          command.intent.sourceLookupURL,
          command.intent.identity,
          command.intent.releaseName,
          { ...command.intent.playlist, Selected: [...command.intent.playlist.Selected] },
          command.controls.confirmBDMVRescan,
          command.signal,
        );
      case "reset":
        return await preparationClient.resetMetadata(
          command.correlationID,
          command.sourcePath,
          command.intent.sourceLookupURL,
          command.intent.identity,
          command.intent.releaseName,
          { ...command.intent.playlist, Selected: [...command.intent.playlist.Selected] },
          command.controls.confirmBDMVRescan,
          command.signal,
        );
      case "candidate":
        return await preparationClient.selectBlurayCandidate(
          command.correlationID,
          command.sourcePath,
          command.releaseID || "",
          command.signal,
        );
    }
  } finally {
    off();
  }
};

const uploadImages: ReleaseSessionPorts["uploadedImages"]["upload"] = async (command) => {
  const off = subscribeWebEvent(imageUploadProgressEvent, (payload: unknown) => {
    const update = payload as ImageUploadProgressUpdate;
    if (update?.correlationID === command.correlationID) command.onProgress(update);
  });
  try {
    return await uploadedImageClient.upload(
      command.correlationID,
      command.release,
      [...command.trackers],
      command.host,
      [...command.images],
      command.signal,
    );
  } finally {
    off();
  }
};

/** Composes production transports once at the application boundary. */
export const productionReleaseSessionPorts = (): ReleaseSessionPorts => ({
  preparation: {
    detectDiscType: (sourcePath, signal) => preparationClient.detectDiscType(sourcePath, signal),
    discoverPlaylists: (sourcePath, signal) => playlistClient.discover(sourcePath, signal),
    execute: executePreparation,
  },
  screenshots: {
    load: (release, signal) => screenshotClient.fetchPlan(release, signal),
    generate: (release, selections, purpose, signal) =>
      screenshotClient.generate(release, [...selections], purpose, signal),
    previewFrame: (release, timestampSeconds, signal) =>
      screenshotClient.previewFrame(release, timestampSeconds, signal),
    remove: (release, imagePath, signal) => screenshotClient.remove(release, imagePath, signal),
    removeTrackerURL: (release, url, signal) =>
      screenshotClient.deleteTrackerImageURL(release, url, signal),
    saveFinal: (release, images, signal) =>
      screenshotClient.saveFinalSelections(release, [...images], signal),
    readImage: (path, signal) => screenshotClient.readImage(path, signal),
  },
  menuImages: {
    list: (release, signal) => menuImageClient.list(release, signal),
    readImage: (path, signal) => screenshotClient.readImage(path, signal),
    importPaths: (release, paths, signal) =>
      menuImageClient.importPaths(release, [...paths], signal),
    capture: (release, signal) => menuImageClient.capture(release, signal),
    remove: (release, imagePath, signal) => menuImageClient.remove(release, imagePath, signal),
  },
  uploadedImages: {
    listCandidates: (release, signal) => uploadedImageClient.listCandidates(release, signal),
    readImage: (path, signal) => screenshotClient.readImage(path, signal),
    listUploaded: (release, signal) => uploadedImageClient.listUploaded(release, signal),
    upload: uploadImages,
    remove: (release, imagePath, host, signal) =>
      uploadedImageClient.remove(release, imagePath, host, signal),
  },
  descriptions: {
    load: (release, trackers, signal) =>
      preparationClient.fetchDescriptionBuilder(release, [...trackers], signal),
    render: (raw, signal) => descriptionClient.render(raw, signal),
    save: (release, groupKey, raw, trackers, signal) =>
      descriptionClient.saveOverride(release, groupKey, raw, [...trackers], signal),
  },
  upload: {
    dryRun: (command, signal) =>
      preparationClient.fetchTrackerDryRun(
        command.dupeJobID,
        command.release,
        [...command.trackers],
        [...command.ignoreDupesFor],
        Object.fromEntries(
          Object.entries(command.questionnaireAnswers).map(([tracker, values]) => [
            tracker,
            { ...values },
          ]),
        ),
        [...command.descriptionGroups],
        command.options.noSeed,
        command.options.runLogLevel,
        signal,
      ),
    review: (command, signal) =>
      uploadClient.review(
        command.release,
        [...command.trackers],
        [...command.ignoreDupesFor],
        Object.fromEntries(
          Object.entries(command.questionnaireAnswers).map(([tracker, values]) => [
            tracker,
            { ...values },
          ]),
        ),
        [...command.descriptionGroups],
        command.options.noSeed,
        command.options.runLogLevel,
        signal,
      ),
  },
});
