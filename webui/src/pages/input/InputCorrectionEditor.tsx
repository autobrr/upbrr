// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { ChangeEvent, ReactNode } from "react";
import { useEffect, useRef, useState } from "react";
import type {
  ContentBinding,
  CorrectionFieldRef,
  ExternalIDOverrides,
  MetadataOverrides,
  ReleaseNameOverrides,
  TrackerQuestionnaireField,
} from "../../api/generated/release-workflow";
import type { InputFacet } from "../../releaseSession/types";
import type { PreparedRelease } from "../../types";
import { Button } from "../../components/ui/button";

const hasOwn = (value: object, key: PropertyKey) =>
  Object.prototype.hasOwnProperty.call(value, key);

const commaValues = (value: string) =>
  value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);

const listText = (value: readonly string[] | null | undefined) => (value || []).join(", ");

const refFor = (field: string, trackId = ""): CorrectionFieldRef => ({
  field,
  ...(trackId ? { trackId } : {}),
});

function CommaListInput({
  id,
  label,
  value,
  onChange,
}: Readonly<{
  id: string;
  label: string;
  value: readonly string[] | null | undefined;
  onChange: (value: readonly string[]) => void;
}>) {
  const rendered = listText(value);
  const [draft, setDraft] = useState(rendered);
  const focused = useRef(false);
  useEffect(() => {
    if (!focused.current) setDraft(rendered);
  }, [rendered]);
  return (
    <input
      id={id}
      aria-label={label}
      value={draft}
      onFocus={() => {
        focused.current = true;
      }}
      onBlur={() => {
        focused.current = false;
      }}
      onChange={(event) => {
        setDraft(event.target.value);
        onChange(commaValues(event.target.value));
      }}
      placeholder="English, Brazilian Portuguese"
    />
  );
}

function ProviderIDInput({
  id,
  label,
  value,
  imdb,
  onChange,
}: Readonly<{
  id: string;
  label: string;
  value: number;
  imdb: boolean;
  onChange: (value: number) => void;
}>) {
  const rendered = value > 0 ? String(value) : "";
  const [draft, setDraft] = useState(rendered);
  const valid = imdb ? /^(?:tt)?\d*$/i.test(draft) : /^\d*$/.test(draft);
  const focused = useRef(false);
  useEffect(() => {
    if (!focused.current) setDraft(rendered);
  }, [rendered]);
  return (
    <div className="min-w-0 flex-1">
      <input
        id={id}
        className="w-full min-w-0"
        aria-label={label}
        aria-invalid={!valid}
        type="text"
        inputMode="numeric"
        pattern={imdb ? "(?:tt)?[0-9]*" : "[0-9]*"}
        value={draft}
        onFocus={() => {
          focused.current = true;
        }}
        onBlur={() => {
          focused.current = false;
          if (valid) setDraft(rendered);
        }}
        onChange={(event) => {
          const entered = event.target.value.trim();
          setDraft(entered);
          const numeric = imdb ? entered.replace(/^tt/i, "") : entered;
          if (/^\d*$/.test(numeric)) onChange(numeric ? Number(numeric) : 0);
        }}
        placeholder="Numeric provider ID"
      />
      {!valid ? (
        <span className="error text-xs" role="alert">
          {imdb ? "Enter digits with an optional tt prefix." : "Enter digits only."}
        </span>
      ) : null}
    </div>
  );
}

function CorrectionRow({
  field,
  label,
  manual,
  stale,
  trackId,
  onAuto,
  onConfirm,
  children,
}: Readonly<{
  field: string;
  label: string;
  manual: boolean;
  stale: boolean;
  trackId?: string;
  onAuto: () => void;
  onConfirm: () => void;
  children: ReactNode;
}>) {
  return (
    <div
      className="settings-field"
      data-correction-field={field}
      {...(trackId ? { "data-track-id": trackId } : {})}
    >
      <label htmlFor={`correction-${field}-${trackId || "value"}`}>{label}</label>
      <div className="flex min-w-0 items-center gap-2">
        <div className="min-w-0 flex-1">{children}</div>
        <Button type="button" className="shrink-0" onClick={onAuto}>
          Auto {label}
        </Button>
      </div>
      <span className="text-xs text-[var(--muted)]">
        {manual ? "Manual value" : "Automatic value"}
        {stale ? " · Saved value needs confirmation" : ""}
      </span>
      {stale ? (
        <Button type="button" onClick={onConfirm}>
          Confirm saved {label}
        </Button>
      ) : null}
    </div>
  );
}

function TriStateField({
  field,
  label,
  value,
  automatic,
  stale,
  onChange,
  onAuto,
  onConfirm,
}: Readonly<{
  field: string;
  label: string;
  value: boolean | null | undefined;
  automatic?: boolean;
  stale: boolean;
  onChange: (value: boolean) => void;
  onAuto: () => void;
  onConfirm: () => void;
}>) {
  const id = `correction-${field}-value`;
  return (
    <div className="settings-field" data-correction-field={field}>
      <label htmlFor={id}>{label}</label>
      <select
        id={id}
        aria-label={label}
        value={value === undefined || value === null ? "auto" : value ? "yes" : "no"}
        onChange={(event) => {
          if (event.target.value === "auto") onAuto();
          else onChange(event.target.value === "yes");
        }}
      >
        <option value="auto">Auto</option>
        <option value="yes">Yes</option>
        <option value="no">No</option>
      </select>
      <span className="text-xs text-[var(--muted)]">
        {value === undefined || value === null
          ? `Automatic${automatic === undefined ? "" : automatic ? ": Yes" : ": No"}`
          : "Manual value"}
        {stale ? " · Saved value needs confirmation" : ""}
      </span>
      {stale ? (
        <Button type="button" onClick={onConfirm}>
          Confirm saved {label}
        </Button>
      ) : null}
    </div>
  );
}

const identityFields: ReadonlyArray<{
  field: string;
  label: string;
  key: keyof ExternalIDOverrides;
}> = [
  { field: "identity.tmdb", label: "TMDB ID", key: "TMDBID" },
  { field: "identity.imdb", label: "IMDB ID", key: "IMDBID" },
  { field: "identity.tvdb", label: "TVDB ID", key: "TVDBID" },
  { field: "identity.tvmaze", label: "TVmaze ID", key: "TVmazeID" },
  { field: "identity.mal", label: "MAL ID", key: "MALID" },
];

const releaseStringFields: ReadonlyArray<{
  field: string;
  label: string;
  key: keyof ReleaseNameOverrides;
  automatic: (release: PreparedRelease | null) => string | number;
}> = [
  {
    field: "release_name.category",
    label: "Category",
    key: "Category",
    automatic: (r) => r?.Identity?.Category || "",
  },
  {
    field: "release_name.type",
    label: "Type",
    key: "Type",
    automatic: (r) => r?.Naming?.Type || "",
  },
  {
    field: "release_name.source",
    label: "Source",
    key: "Source",
    automatic: (r) => r?.Naming?.Source || "",
  },
  {
    field: "release_name.resolution",
    label: "Resolution",
    key: "Resolution",
    automatic: (r) => r?.Naming?.Resolution || "",
  },
  {
    field: "release_name.tag",
    label: "Release group",
    key: "Tag",
    automatic: (r) => r?.Naming?.Tag || "",
  },
  {
    field: "release_name.service",
    label: "Service",
    key: "Service",
    automatic: (r) => r?.Media?.Service || "",
  },
  {
    field: "release_name.edition",
    label: "Edition",
    key: "Edition",
    automatic: (r) => r?.Media?.Edition || "",
  },
  {
    field: "release_name.season",
    label: "Season",
    key: "Season",
    automatic: (r) => r?.Episode?.SeasonLabel || "",
  },
  {
    field: "release_name.episode",
    label: "Episode",
    key: "Episode",
    automatic: (r) => r?.Episode?.EpisodeLabel || "",
  },
  {
    field: "release_name.episode_title",
    label: "Episode title",
    key: "EpisodeTitle",
    automatic: (r) => r?.Episode?.Title || "",
  },
  {
    field: "release_name.manual_year",
    label: "Manual year",
    key: "ManualYear",
    automatic: (r) => r?.Naming?.Year || "",
  },
  {
    field: "release_name.manual_date",
    label: "Manual date",
    key: "ManualDate",
    automatic: (r) => r?.Episode?.DailyDate || "",
  },
  {
    field: "release_name.region",
    label: "Region",
    key: "Region",
    automatic: (r) => r?.Naming?.Region || r?.Media?.Region || "",
  },
];

const releaseBooleanFields: ReadonlyArray<{
  field: string;
  label: string;
  key: keyof ReleaseNameOverrides;
}> = [
  {
    field: "release_name.use_season_episode",
    label: "Use season and episode",
    key: "UseSeasonEpisode",
  },
  { field: "release_name.no_season", label: "No season", key: "NoSeason" },
  { field: "release_name.no_year", label: "No year", key: "NoYear" },
  { field: "release_name.no_aka", label: "No AKA", key: "NoAKA" },
  { field: "release_name.no_tag", label: "No tag", key: "NoTag" },
  { field: "release_name.no_episode_title", label: "No episode title", key: "NoEpisodeTitle" },
  { field: "release_name.no_distributor", label: "No distributor", key: "NoDistributor" },
  { field: "release_name.no_edition", label: "No edition", key: "NoEdition" },
  { field: "release_name.no_dub", label: "No dub", key: "NoDub" },
  { field: "release_name.no_dual", label: "No dual audio", key: "NoDual" },
  { field: "release_name.dual_audio", label: "Force dual audio", key: "DualAudio" },
];

const metadataStringFields: ReadonlyArray<{
  field: string;
  label: string;
  key: keyof MetadataOverrides;
  automatic: (release: PreparedRelease | null) => string;
}> = [
  {
    field: "metadata.distributor",
    label: "Distributor",
    key: "Distributor",
    automatic: (r) => r?.Media?.Distributor || "",
  },
  {
    field: "metadata.original_language",
    label: "Original language",
    key: "OriginalLanguage",
    automatic: (r) => r?.Media?.OriginalLanguage || "",
  },
  {
    field: "metadata.title",
    label: "Title",
    key: "Title",
    automatic: (r) => r?.Naming?.Title || "",
  },
  {
    field: "metadata.alternate_title",
    label: "Alternate title",
    key: "AlternateTitle",
    automatic: (r) => r?.Naming?.AlternateTitle || "",
  },
  {
    field: "metadata.original_title",
    label: "Original title",
    key: "OriginalTitle",
    automatic: (r) => r?.Naming?.OriginalTitle || "",
  },
];

const metadataListFields: ReadonlyArray<{
  field: string;
  label: string;
  key: keyof MetadataOverrides;
  automatic: (release: PreparedRelease | null) => readonly string[];
}> = [
  {
    field: "metadata.genres",
    label: "Genres",
    key: "Genres",
    automatic: (r) => r?.Naming?.Genres || [],
  },
  {
    field: "metadata.audio_languages",
    label: "Audio languages",
    key: "AudioLanguages",
    automatic: (r) => r?.Media?.AudioLanguages || [],
  },
  {
    field: "metadata.subtitle_languages",
    label: "Subtitle languages",
    key: "SubtitleLanguages",
    automatic: (r) => r?.Media?.SubtitleLanguages || [],
  },
  {
    field: "metadata.hardcoded_subtitle_languages",
    label: "Hardcoded subtitle languages",
    key: "HardcodedSubtitleLanguages",
    automatic: (r) => r?.Media?.HardcodedSubtitleLanguages || [],
  },
];

const metadataBooleanFields: ReadonlyArray<{
  field: string;
  label: string;
  key: keyof MetadataOverrides;
  automatic: (release: PreparedRelease | null) => boolean;
}> = [
  {
    field: "metadata.personal_release",
    label: "Personal release",
    key: "PersonalRelease",
    automatic: (r) => Boolean(r?.Naming?.Personal),
  },
  {
    field: "metadata.commentary",
    label: "Commentary",
    key: "Commentary",
    automatic: (r) => Boolean(r?.Media?.Commentary),
  },
  {
    field: "metadata.web_dv",
    label: "WEB Dolby Vision",
    key: "WebDV",
    automatic: (r) => Boolean(r?.Media?.WebDV),
  },
  {
    field: "metadata.stream_optimized",
    label: "Stream optimized",
    key: "StreamOptimized",
    automatic: (r) => Boolean(r?.Media?.StreamOptimized),
  },
  {
    field: "metadata.anime",
    label: "Anime",
    key: "Anime",
    automatic: (r) => Boolean(r?.Media?.Anime),
  },
  {
    field: "metadata.hardcoded_subs",
    label: "Hardcoded subtitles",
    key: "HardcodedSubs",
    automatic: (r) => Boolean(r?.Media?.HardcodedSubs),
  },
];

const optionLabel = (value: string) =>
  value ? `${value.slice(0, 1).toUpperCase()}${value.slice(1)}` : value;

function TrackerInputField({
  tracker,
  field,
  value,
  onChange,
}: Readonly<{
  tracker: string;
  field: TrackerQuestionnaireField;
  value: string;
  onChange: (value: string | null) => void;
}>) {
  const id = `tracker-input-${tracker.toLowerCase()}-${field.Key}`;
  const label = `${tracker} ${field.Label}`;
  const helpID = field.Help ? `${id}-help` : undefined;
  const shared = {
    id,
    "aria-label": label,
    "aria-describedby": helpID,
    required: field.Required,
    value,
    onChange: (event: ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) =>
      onChange(
        field.Kind === "select" && event.target.value === "auto" ? null : event.target.value,
      ),
  };
  return (
    <div className="settings-field" data-tracker-input={`${tracker}:${field.Key}`}>
      <label htmlFor={id}>
        {field.Label}
        {field.Required ? " *" : ""}
      </label>
      {field.Kind === "select" ? (
        <select {...shared}>
          {field.Options.map((option) => (
            <option key={option} value={option}>
              {optionLabel(option)}
            </option>
          ))}
        </select>
      ) : field.Kind === "textarea" ? (
        <textarea {...shared} placeholder={field.Placeholder} />
      ) : (
        <input {...shared} placeholder={field.Placeholder} />
      )}
      {field.Help ? (
        <span id={helpID} className="text-xs text-[var(--muted)]">
          {field.Help}
        </span>
      ) : null}
    </div>
  );
}

const bindingSummary = (binding: ContentBinding | undefined) => {
  if (!binding) return "unknown prior content";
  const providerIDs = Object.entries(binding.providerIds)
    .filter(([, value]) => value > 0)
    .map(([provider, value]) => `${provider.replace(/Id$/, "").toUpperCase()} ${value}`)
    .join(", ");
  return `${binding.category || "unknown category"}${providerIDs ? ` · ${providerIDs}` : ""}`;
};

/** Renders typed manual corrections while the release session owns draft and transport state. */
export function InputCorrectionEditor({ facet }: Readonly<{ facet: InputFacet }>) {
  const { view } = facet;
  const release = view.release;
  const staleFields = new Set(view.corrections?.corrections.staleContentFields || []);

  const reset = (field: string, trackId = "") => facet.resetCorrection(refFor(field, trackId));
  const confirm = (field: string, trackId = "") => facet.confirmCorrection(refFor(field, trackId));

  const identityAutomatic = (key: keyof ExternalIDOverrides) => Number(release?.Identity[key] || 0);
  const setIdentity = (key: keyof ExternalIDOverrides, value: number) =>
    facet.changeIdentity({ ...view.intent.identity, [key]: value });
  const setReleaseName = (key: keyof ReleaseNameOverrides, value: string | number | boolean) =>
    facet.changeReleaseName({ ...view.intent.releaseName, [key]: value });
  const setMetadata = (key: keyof MetadataOverrides, value: string | boolean | readonly string[]) =>
    facet.changeMetadata({ ...view.intent.metadata, [key]: value });

  return (
    <div className="grid gap-4" data-testid="input-correction-editor">
      <div className="settings-subgroup">
        <div className="settings-subgroup__title">Provider IDs</div>
        <div className="settings-grid">
          {identityFields.map(({ field, label, key }) => {
            const manual = hasOwn(view.intent.identity, key);
            const manualValue = view.intent.identity[key];
            const value = manual ? Number(manualValue || 0) : identityAutomatic(key);
            return (
              <CorrectionRow
                key={field}
                field={field}
                label={label}
                manual={manual}
                stale={staleFields.has(field)}
                onAuto={() => reset(field)}
                onConfirm={() => confirm(field)}
              >
                <div className="flex min-w-0 items-center gap-2">
                  <ProviderIDInput
                    id={`correction-${field}-value`}
                    label={label}
                    value={value}
                    imdb={key === "IMDBID"}
                    onChange={(next) => setIdentity(key, next)}
                  />
                  <Button
                    type="button"
                    className="shrink-0"
                    aria-label={`Remove ${label}`}
                    disabled={manual && value === 0}
                    onClick={() => setIdentity(key, 0)}
                  >
                    {manual && value === 0 ? "Removed" : "Remove"}
                  </Button>
                </div>
              </CorrectionRow>
            );
          })}
        </div>
      </div>

      <div className="settings-subgroup">
        <div className="settings-subgroup__title">Release name</div>
        <div className="settings-grid">
          {releaseStringFields.map(({ field, label, key, automatic }) => {
            const manual = hasOwn(view.intent.releaseName, key);
            const rawValue = manual ? view.intent.releaseName[key] : automatic(release);
            const numeric = key === "ManualYear";
            return (
              <CorrectionRow
                key={field}
                field={field}
                label={label}
                manual={manual}
                stale={staleFields.has(field)}
                onAuto={() => reset(field)}
                onConfirm={() => confirm(field)}
              >
                <input
                  id={`correction-${field}-value`}
                  aria-label={label}
                  type={numeric ? "number" : "text"}
                  value={
                    rawValue === null || rawValue === undefined || rawValue === 0
                      ? ""
                      : String(rawValue)
                  }
                  onChange={(event) =>
                    setReleaseName(
                      key,
                      numeric
                        ? event.target.value
                          ? Number(event.target.value)
                          : 0
                        : event.target.value,
                    )
                  }
                />
              </CorrectionRow>
            );
          })}
          {releaseBooleanFields.map(({ field, label, key }) => (
            <TriStateField
              key={field}
              field={field}
              label={label}
              value={
                hasOwn(view.intent.releaseName, key)
                  ? (view.intent.releaseName[key] as boolean | null | undefined)
                  : undefined
              }
              stale={staleFields.has(field)}
              onChange={(value) => setReleaseName(key, value)}
              onAuto={() => reset(field)}
              onConfirm={() => confirm(field)}
            />
          ))}
        </div>
      </div>

      <div className="settings-subgroup">
        <div className="settings-subgroup__title">Metadata and languages</div>
        <div className="settings-grid">
          {metadataStringFields.map(({ field, label, key, automatic }) => {
            const manual = hasOwn(view.intent.metadata, key);
            const value = manual ? view.intent.metadata[key] : automatic(release);
            return (
              <CorrectionRow
                key={field}
                field={field}
                label={label}
                manual={manual}
                stale={staleFields.has(field)}
                onAuto={() => reset(field)}
                onConfirm={() => confirm(field)}
              >
                <input
                  id={`correction-${field}-value`}
                  aria-label={label}
                  value={typeof value === "string" ? value : ""}
                  onChange={(event) => setMetadata(key, event.target.value)}
                />
              </CorrectionRow>
            );
          })}
          {metadataListFields.map(({ field, label, key, automatic }) => {
            const manual = hasOwn(view.intent.metadata, key);
            const value = manual
              ? (view.intent.metadata[key] as readonly string[] | null | undefined)
              : automatic(release);
            return (
              <CorrectionRow
                key={field}
                field={field}
                label={label}
                manual={manual}
                stale={staleFields.has(field)}
                onAuto={() => reset(field)}
                onConfirm={() => confirm(field)}
              >
                <CommaListInput
                  id={`correction-${field}-value`}
                  label={label}
                  value={value}
                  onChange={(next) => setMetadata(key, next)}
                />
              </CorrectionRow>
            );
          })}
          {metadataBooleanFields.map(({ field, label, key, automatic }) => (
            <TriStateField
              key={field}
              field={field}
              label={label}
              value={
                hasOwn(view.intent.metadata, key)
                  ? (view.intent.metadata[key] as boolean | null | undefined)
                  : undefined
              }
              automatic={automatic(release)}
              stale={staleFields.has(field)}
              onChange={(value) => setMetadata(key, value)}
              onAuto={() => reset(field)}
              onConfirm={() => confirm(field)}
            />
          ))}
        </div>
      </div>

      <div className="settings-subgroup" data-testid="input-track-coverage">
        <div className="settings-subgroup__title">Inspected tracks</div>
        <p className="muted">
          {release?.Media?.TrackCoverageComplete
            ? "Track coverage is complete."
            : "Track coverage is incomplete; corrections apply only to inspected resources."}
        </p>
        {(release?.Media?.Tracks || []).length === 0 ? (
          <p className="muted">No inspected audio or subtitle tracks.</p>
        ) : (
          <div className="settings-grid">
            {(release?.Media?.Tracks || []).map((track, index) => {
              const field = "metadata.track_languages";
              const correction = (view.intent.metadata.TrackLanguages || []).find(
                (item) => item.trackId === track.ID,
              );
              const ordinal =
                track.Ordinal > 0
                  ? track.Ordinal
                  : (release?.Media?.Tracks || [])
                      .slice(0, index + 1)
                      .filter((candidate) => candidate.Kind === track.Kind).length;
              const label = `${track.Kind === "audio" ? "Audio" : "Subtitle"} track ${ordinal} languages`;
              return (
                <CorrectionRow
                  key={track.ID}
                  field={field}
                  trackId={track.ID}
                  label={label}
                  manual={Boolean(correction)}
                  stale={false}
                  onAuto={() => reset(field, track.ID)}
                  onConfirm={() => confirm(field, track.ID)}
                >
                  <CommaListInput
                    id={`correction-${field}-${track.ID}`}
                    label={label}
                    value={correction?.languages || track.Languages}
                    onChange={(languages) => {
                      const retained = (view.intent.metadata.TrackLanguages || []).filter(
                        (item) => item.trackId !== track.ID,
                      );
                      facet.changeMetadata({
                        ...view.intent.metadata,
                        TrackLanguages: [
                          ...retained,
                          {
                            trackId: track.ID,
                            languages,
                            manifestFingerprint: track.ManifestFingerprint,
                          },
                        ],
                      });
                    }}
                  />
                  <span className="text-xs text-[var(--muted)]">
                    Track ID: {track.ID} · Resource: {track.ResourceID || "unknown"} · Detected:{" "}
                    {listText(track.DetectedLanguages) || "unknown"}
                  </span>
                </CorrectionRow>
              );
            })}
          </div>
        )}
      </div>

      <div className="settings-subgroup" data-testid="input-source-options">
        <div className="settings-subgroup__title">Source options</div>
        <div className="settings-grid">
          {view.selectedTrackers.map((tracker) => (
            <div className="settings-field" key={tracker} data-tracker-source-id={tracker}>
              <label htmlFor={`tracker-source-${tracker}`}>{tracker} source ID</label>
              <input
                id={`tracker-source-${tracker}`}
                value={view.intent.trackerSourceIDs[tracker] || ""}
                onChange={(event) => facet.changeTrackerSourceID(tracker, event.target.value)}
              />
            </div>
          ))}
          <label className="settings-toggle">
            <span>Keep source folder</span>
            <input
              type="checkbox"
              checked={view.intent.policy.keepFolder}
              onChange={(event) =>
                facet.changePreparationPolicy({
                  ...view.intent.policy,
                  keepFolder: event.target.checked,
                })
              }
            />
          </label>
          <label className="settings-toggle">
            <span>Keep images</span>
            <input
              type="checkbox"
              checked={view.intent.policy.keepImages}
              onChange={(event) =>
                facet.changePreparationPolicy({
                  ...view.intent.policy,
                  keepImages: event.target.checked,
                })
              }
            />
          </label>
          <label className="settings-toggle">
            <span>Only resolve IDs</span>
            <input
              type="checkbox"
              checked={view.intent.policy.onlyID}
              onChange={(event) =>
                facet.changePreparationPolicy({
                  ...view.intent.policy,
                  onlyID: event.target.checked,
                })
              }
            />
          </label>
          <label className="settings-toggle">
            <span>Skip client search</span>
            <input
              type="checkbox"
              checked={view.intent.search.skip}
              onChange={(event) =>
                facet.changeClientSearch({ ...view.intent.search, skip: event.target.checked })
              }
            />
          </label>
          <div className="settings-field">
            <label htmlFor="input-client-search">Client search name</label>
            <input
              id="input-client-search"
              value={view.intent.search.client}
              disabled={view.intent.search.skip}
              onChange={(event) =>
                facet.changeClientSearch({ ...view.intent.search, client: event.target.value })
              }
            />
          </div>
        </div>
      </div>

      {(view.readiness?.schemas || []).length > 0 ? (
        <div className="settings-subgroup" data-testid="input-tracker-fields">
          <div className="settings-subgroup__title">Tracker Input</div>
          {(view.readiness?.schemas || []).map((schema) => (
            <section key={schema.Tracker} aria-label={`${schema.Tracker} Input fields`}>
              <h4 className="mb-2 text-sm font-semibold">{schema.Tracker}</h4>
              <div className="settings-grid">
                {schema.Fields.map((field) => (
                  <TrackerInputField
                    key={field.Key}
                    tracker={schema.Tracker}
                    field={field}
                    value={
                      view.trackerInputAnswers[schema.Tracker]?.[field.Key] === null
                        ? "auto"
                        : (view.trackerInputAnswers[schema.Tracker]?.[field.Key] ?? field.Value)
                    }
                    onChange={(value) =>
                      facet.changeTrackerInputAnswer(schema.Tracker, field.Key, value)
                    }
                  />
                ))}
              </div>
            </section>
          ))}
        </div>
      ) : null}

      {view.readiness ? (
        <div className="settings-subgroup" data-testid="input-readiness">
          <div className="settings-subgroup__title">Input readiness</div>
          <p className="muted">Status: {view.readiness.status}</p>
          {(view.readiness.fields || []).length === 0 ? (
            <p className="muted">No missing Input fields.</p>
          ) : (
            <ul className="grid gap-2">
              {(view.readiness.fields || []).map((field) => (
                <li
                  key={`${field.key}-${(field.trackerIds || []).join("-")}`}
                  data-readiness-field={field.key}
                >
                  <strong>{field.key}</strong>: {field.status}
                  {field.trackerIds?.length ? ` · ${field.trackerIds.join(", ")}` : ""}
                  {field.message ? ` · ${field.message}` : ""}
                </li>
              ))}
            </ul>
          )}
          {(view.readiness.requiredActions || [])
            .filter((action) => action.status === "pending")
            .map((action) => (
              <div key={action.id} className="grid gap-1" data-input-action={action.id}>
                <p className="error">{action.prompt}</p>
                {action.correctionConfirmation?.fields.map((field) => (
                  <p key={field} className="muted" data-stale-evidence={field}>
                    {field}: saved for{" "}
                    {bindingSummary(action.correctionConfirmation?.previousBindings[field])};
                    current {bindingSummary(action.correctionConfirmation?.currentBinding)}.
                  </p>
                ))}
              </div>
            ))}
        </div>
      ) : null}
    </div>
  );
}
