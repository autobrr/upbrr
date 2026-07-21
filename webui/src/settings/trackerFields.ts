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
  LinkDirName: stringField("LinkDirName", { label: "Link dir name", advanced: true }),
  APIKey: stringField("APIKey", { label: "API key", sensitive: true }),
  ApiKey: stringField("ApiKey", { label: "API key", sensitive: true }),
  ApiUser: stringField("ApiUser", { label: "API user", sensitive: true }),
  Username: stringField("Username", { label: "Username" }),
  Password: stringField("Password", { label: "Password", sensitive: true }),
  Passkey: stringField("Passkey", { label: "Passkey", sensitive: true }),
  AnnounceURL: stringField("AnnounceURL", { label: "Announce URL", sensitive: true }),
  MyAnnounceURL: stringField("MyAnnounceURL", {
    label: "My announce URL",
    sensitive: true,
  }),
  FaviconURL: stringField("FaviconURL", { label: "Favicon URL", advanced: true }),
  UploaderName: stringField("UploaderName", { label: "Uploader name" }),
  UploaderStatus: boolField("UploaderStatus", { label: "Uploader status" }),
  CustomLayout: stringField("CustomLayout", { label: "Custom layout" }),
  TagForCustomRelease: stringField("TagForCustomRelease", { label: "Tag for custom release" }),
  CheckForRules: boolField("CheckForRules", { label: "Check for rules" }),
  ModQ: boolField("ModQ", { label: "Mod queue" }),
  Draft: boolField("Draft", { label: "Draft" }),
  DraftDefault: boolField("DraftDefault", { label: "Draft default" }),
  Anon: boolField("Anon", { label: "Anonymous" }),
  ShowGroupIfAnon: boolField("ShowGroupIfAnon", { label: "Show group if anon" }),
  BhdRSSKey: stringField("BhdRSSKey", { label: "BHD RSS key", sensitive: true }),
  CheckRequests: boolField("CheckRequests", { label: "Check requests" }),
  FullMediainfo: boolField("FullMediainfo", { label: "Full mediainfo" }),
  ImgRehost: boolField("ImgRehost", { label: "Image rehost" }),
  ImageHost: stringField("ImageHost", { label: "Image host" }),
  TorrentClient: stringField("TorrentClient", { label: "Torrent client", advanced: true }),
  UseSpanishTitle: boolField("UseSpanishTitle", { label: "Use Spanish title" }),
  UseItalianTitle: boolField("UseItalianTitle", { label: "Use Italian title" }),
  OTPURI: stringField("OTPURI", { label: "OTP URI", sensitive: true }),
  SkipIfRehash: boolField("SkipIfRehash", { label: "Skip if rehash", advanced: true }),
  PreferMTV: boolField("PreferMTV", { label: "Prefer MTV torrent", advanced: true }),
  PTGenAPI: stringField("PTGenAPI", { label: "PTGen API", sensitive: true }),
  AddWebSourceToDesc: boolField("AddWebSourceToDesc", { label: "Add web source to desc" }),
  UseMetadataName: boolField("UseMetadataName", { label: "Use metadata name" }),
  InjectDelay: numberField("InjectDelay", { label: "Inject delay", advanced: true }),
  ImageCount: numberField("ImageCount", { label: "Image count" }),
  Channel: stringField("Channel", { label: "Channel" }),
  ImgAPI: stringField("ImgAPI", { label: "Image API", sensitive: true }),
  PronfoAPIKey: stringField("PronfoAPIKey", { label: "Pronfo API key", sensitive: true }),
  PronfoTheme: stringField("PronfoTheme", { label: "Pronfo theme" }),
  PronfoRAPIID: stringField("PronfoRAPIID", { label: "Pronfo RAPI ID" }),
  APIUpload: boolField("APIUpload", { label: "API upload" }),
  Exclusive: boolField("Exclusive", { label: "Exclusive" }),
  LoginQuestion: stringField("LoginQuestion", { label: "Login question", sensitive: true }),
  LoginAnswer: stringField("LoginAnswer", { label: "Login answer", sensitive: true }),
  UserID: stringField("UserID", { label: "User ID", sensitive: true }),
  Internal: boolField("Internal", { label: "Internal" }),
  InternalGroups: stringField("InternalGroups", { label: "Internal groups" }),
};

/** Resolves a catalog field to renderer metadata and rejects schema drift. */
export const trackerFieldPresentation = (key: string): FieldMeta => {
  const field = trackerFieldMeta[key];
  if (!field) throw new Error(`Unsupported tracker config field: ${key}`);
  return field;
};
