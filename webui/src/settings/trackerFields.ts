// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import type { FieldMeta } from "../types";

const stringField = (key: string, meta: Omit<FieldMeta, "key" | "type"> = {}): FieldMeta => ({
  key,
  type: "string",
  ...meta,
});

const boolField = (key: string, meta: Omit<FieldMeta, "key" | "type"> = {}): FieldMeta => ({
  key,
  type: "boolean",
  ...meta,
});

const numberField = (key: string, meta: Omit<FieldMeta, "key" | "type"> = {}): FieldMeta => ({
  key,
  type: "number",
  ...meta,
});

/** Generic presentation metadata for config-catalog tracker fields. */
const trackerFieldMeta: Record<string, FieldMeta> = {
  LinkDirName: stringField("LinkDirName", {
    label: "Link staging subfolder",
    advanced: true,
    help: "Subfolder below the client linked folder for this tracker. Empty uses the tracker name. Only used when client file linking is enabled.",
  }),
  APIKey: stringField("APIKey", {
    label: "API key",
    sensitive: true,
    help: "This account's tracker API credential for supported API operations. Other session or cookie requirements still apply. Leave [REDACTED] unchanged to retain a stored key.",
  }),
  ApiKey: stringField("ApiKey", {
    label: "API key",
    sensitive: true,
    help: "Tracker API credential paired with the API username. Leave [REDACTED] unchanged to retain a stored key.",
  }),
  ApiUser: stringField("ApiUser", {
    label: "API username",
    sensitive: true,
    help: "PTP API user identity paired with the API key; this is separate from website login credentials.",
  }),
  Username: stringField("Username", {
    label: "Account username",
    help: "Tracker account identity used by supported authentication operations. Some trackers pair it with a passkey rather than a password.",
  }),
  Password: stringField("Password", {
    label: "Account password",
    sensitive: true,
    help: "Tracker website login password where supported, not an API key or passkey. Leave [REDACTED] unchanged to preserve it.",
  }),
  Passkey: stringField("Passkey", {
    label: "Account passkey",
    sensitive: true,
    help: "Tracker-issued account passkey for supported authenticated operations. Some trackers also require a username or cookies.",
  }),
  AnnounceURL: stringField("AnnounceURL", {
    label: "Personal announce URL",
    sensitive: true,
    help: "Personal announce URL supplied by the tracker for torrent artifacts. Check that it belongs to the correct account; upbrr cannot determine whether the personal credential is yours.",
  }),
  MyAnnounceURL: stringField("MyAnnounceURL", {
    label: "BHDTV personal announce URL",
    sensitive: true,
    help: "BHDTV personal announce URL required for its upload torrent. Use the account-specific URL supplied by BHDTV.",
  }),
  FaviconURL: stringField("FaviconURL", {
    label: "Custom tracker icon URL",
    advanced: true,
    help: "Overrides the icon source when tracker icons are enabled. Empty uses the known tracker domain.",
  }),
  UploaderName: stringField("UploaderName", {
    label: "Displayed uploader name",
    help: "FileList uploader-credit value. Not sent when anonymous uploading is enabled.",
  }),
  GuruTrump: boolField("GuruTrump", { label: "Guru+ intended trump uploads" }),
  UploaderStatus: boolField("UploaderStatus", {
    label: "Auto-approve ASC uploads",
    help: "Attempt ASC uploader approval after upload. Requires account permission; enabling this does not grant uploader privileges.",
  }),
  CustomLayout: stringField("CustomLayout", {
    label: "ASC description layout ID",
    help: "ASC layout ID used to build the description and submit the upload. Empty uses layout 2.",
  }),
  TagForCustomRelease: stringField("TagForCustomRelease", {
    label: "Custom release group tag",
    help: "For supported localized UNIT3D naming, preserve the original group before this tag when the reviewed group matches it. Does not universally replace the release group.",
  }),
  CheckForRules: boolField("CheckForRules", {
    label: "Rule-check preference (compatibility)",
    help: "Retained for compatibility; not currently used to switch tracker rule validation on or off.",
  }),
  ModQ: boolField("ModQ", {
    label: "Opt into moderation queue",
    help: "Send the moderation-queue opt-in for this supported UNIT3D tracker. Does not bypass tracker moderation policy.",
  }),
  Draft: boolField("Draft", {
    label: "Upload as draft",
    help: "Request a non-live BHD submission or the LST draft queue. BHD also uses drafts when its default-draft preference is enabled.",
  }),
  DraftDefault: boolField("DraftDefault", {
    label: "Default BHD uploads to drafts",
    help: "Request non-live BHD submission. Either this preference or Upload as draft requests a draft.",
  }),
  Anon: boolField("Anon", {
    label: "Anonymous upload",
    help: "Request anonymous uploader attribution where implemented. Does not guarantee privacy from the tracker. Current BHDTV and BJS upload payloads do not use this preference.",
  }),
  ShowGroupIfAnon: boolField("ShowGroupIfAnon", {
    label: "Show group when anonymous (compatibility)",
    help: "Retained for compatibility; not currently used to alter group display on anonymous uploads.",
  }),
  BhdRSSKey: stringField("BhdRSSKey", {
    label: "BHD RSS search key",
    sensitive: true,
    help: "Used with the BHD API key for existing-torrent metadata lookup, including direct ID lookup. Both credentials must satisfy the tracker adapter requirements.",
  }),
  CheckRequests: boolField("CheckRequests", {
    label: "Request-search preference (compatibility)",
    help: "Retained for compatibility; not currently used to enable tracker request searches.",
  }),
  FullMediainfo: boolField("FullMediainfo", {
    label: "Full MediaInfo preference (compatibility)",
    help: "Retained for compatibility; not currently used to change MediaInfo report inclusion or formatting.",
  }),
  ImgRehost: boolField("ImgRehost", {
    label: "Rehost TorrentLeech screenshots",
    help: "Enable the TorrentLeech image-host policy and screenshot upload fields. This is not a global rehosting preference.",
  }),
  ImageHost: stringField("ImageHost", {
    label: "Preferred image host for this tracker",
    help: "Choose a host allowed by this tracker. Empty uses its policy and global candidates. This cannot enable disallowed hosts or another tracker's private host.",
  }),
  TorrentClient: stringField("TorrentClient", {
    label: "Client receiving this tracker's torrents",
    advanced: true,
    help: "Override global torrent injection selection for this tracker. Empty follows the global selection. Does not change which clients are searched.",
  }),
  UseSpanishTitle: boolField("UseSpanishTitle", {
    label: "Spanish title preference (compatibility)",
    help: "Retained for compatibility; not currently used to select a Spanish release title.",
  }),
  UseItalianTitle: boolField("UseItalianTitle", {
    label: "Italian title preference (compatibility)",
    help: "Retained for compatibility; not currently used to select an Italian release title.",
  }),
  OTPURI: stringField("OTPURI", {
    label: "Authenticator setup URI",
    sensitive: true,
    help: "Secret reusable TOTP setup URI for supported BTN/PTP login, not the current one-time code. An explicitly submitted challenge code takes precedence.",
  }),
  SkipIfRehash: boolField("SkipIfRehash", {
    label: "Skip when torrent regeneration is needed",
    advanced: true,
    help: "Omit this tracker when preparation requires torrent regeneration, including forced source-data regeneration. This is not a qBittorrent recheck switch.",
  }),
  PTGenAPI: stringField("PTGenAPI", {
    label: "PTGen endpoint (compatibility)",
    sensitive: true,
    help: "Retained for compatibility; not currently used to invoke PTGen.",
  }),
  AddWebSourceToDesc: boolField("AddWebSourceToDesc", {
    label: "Include streaming service in description",
    help: "Include the known streaming service name in PTP WEB-DL descriptions.",
  }),
  UseMetadataName: boolField("UseMetadataName", {
    label: "Metadata-name preference (compatibility)",
    help: "Retained for compatibility; not currently used to choose the release name.",
  }),
  InjectDelay: numberField("InjectDelay", {
    label: "Client injection delay (seconds)",
    advanced: true,
    help: "Override the global client-injection delay for this tracker. An unset value inherits globally; entering zero or a negative value requests no wait.",
  }),
  ImageCount: numberField("ImageCount", {
    label: "Tracker screenshot count",
    help: "Contributes this tracker's screenshot requirement. A non-positive value falls back to the global count when no explicit release count is set. TVC also uses this value as logo width with a 300-pixel minimum, and uses a minimum screenshot count of two when building its description.",
  }),
  Channel: stringField("Channel", {
    label: "SPD channel ID or tag",
    help: "Use a numeric channel ID or a tag resolved during preparation. Empty defaults to channel 1. A release questionnaire answer takes precedence.",
  }),
  APIUpload: boolField("APIUpload", {
    label: "Use TorrentLeech upload API",
    help: "Select the TorrentLeech API upload path instead of its website form.",
  }),
  Exclusive: boolField("Exclusive", {
    label: "Mark upload exclusive",
    help: "Send the exclusive flag for this supported tracker. Does not independently verify that the release is exclusive.",
  }),
  LoginQuestion: stringField("LoginQuestion", {
    label: "Security question (compatibility)",
    sensitive: true,
    help: "Retained for compatibility; not currently used during tracker login.",
  }),
  LoginAnswer: stringField("LoginAnswer", {
    label: "Security answer (compatibility)",
    sensitive: true,
    help: "Retained for compatibility; not currently used during tracker login. Preserve the stored redacted value unless replacing or clearing it.",
  }),
  UserID: stringField("UserID", {
    label: "Account ID (compatibility)",
    sensitive: true,
    help: "Retained for compatibility; not currently used by supported tracker runtime adapters.",
  }),
  DupeBypassGroups: stringField("DupeBypassGroups", {
    label: "Same-group duplicate-check groups",
    commaSeparated: true,
    help: "Comma-separated release groups: confirmed different-group duplicate candidates are suppressed. Does not disable all duplicate checks. Matching ignores case and a leading hyphen.",
  }),
  PersonalReleaseGroups: stringField("PersonalReleaseGroups", {
    label: "Personal release groups",
    commaSeparated: true,
    help: "Matching release groups default this tracker's upload to personal release unless explicitly overridden. This does not imply same-group duplicate filtering.",
  }),
  InternalGroups: stringField("InternalGroups", {
    label: "Internal release groups",
    commaSeparated: true,
    help: "Matching release groups enable tracker-specific internal handling and same-group duplicate filtering. Separate groups with commas; matching ignores case and a leading hyphen.",
  }),
};

/** Resolves a catalog field to renderer metadata and rejects schema drift. */
export const trackerFieldPresentation = (key: string): FieldMeta => {
  const field = trackerFieldMeta[key];
  if (!field) throw new Error(`Unsupported tracker config field: ${key}`);
  return field;
};
