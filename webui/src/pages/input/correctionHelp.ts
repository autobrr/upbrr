// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

/** Presentation only: corrections are applied by preparation, not by opening help. */
export const correctionHelp: Record<string, string> = {
  "release_name.category":
    "Choose Movie or TV for provider lookups and tracker categorization. Changing category can require review of saved content corrections.",
  "release_name.type":
    "How this release was made, such as an encode, remux, web download or full disc. Choose a supported value; this corrects prepared facts used by naming and tracker checks.",
  "release_name.source":
    "The original media source, such as Blu-ray, DVD or a web service. Choose a supported value; this is separate from the source file path.",
  "release_name.resolution":
    "The release resolution used by naming and tracker checks. Choose a supported resolution; changing it does not resize the video.",
  "release_name.tag":
    "The release group used in names and group checks. A leading hyphen is optional. An explicit blank removes the group; Auto restores detection.",
  "release_name.service":
    "The streaming service associated with a web release. Search by name or code; selecting a suggestion stores its recognized code. Custom values and an explicit blank are allowed.",
  "release_name.edition":
    "Cut or edition text, separate from presentation, release features and release version. Recognized compound editions are split into independent facts; custom wording is retained.",
  "release_name.presentation":
    "Select IMAX, Open Matte, both, or None independently of Edition. This changes the prepared presentation annotation, not the video geometry.",
  "release_name.release_features":
    "Select multiple annotations such as Hybrid or 4K Restoration. None explicitly clears the selection; Auto restores detection. These annotations do not change measured media facts.",
  "release_name.repack":
    "Select REPACK, PROPER, a supported numbered variant or RERIP. None removes the detected release version; Auto restores detection. This is independent of Edition.",
  "release_name.season":
    "Correct the TV season using a number or S-prefixed label, including S00 for specials. This changes prepared episode facts; it does not select different source files.",
  "release_name.episode":
    "Correct the TV episode using a number or E-prefixed label. This changes prepared episode facts; it does not select different source files.",
  "release_name.episode_title":
    "Override the episode title used in prepared metadata and generated names. An explicit blank removes it; Auto restores the derived title.",
  "release_name.manual_year":
    "Override the movie release year. Clearing saves zero; Auto restores the derived year. TV year remains provider-owned and cannot be edited here.",
  "release_name.manual_date":
    "Set a daily episode date in YYYY-MM-DD form. Daily-date naming can replace season/episode naming; explicit season-zero specials retain their episode identity.",
  "release_name.region":
    "Correct the release region using a country-code suggestion or a custom value. This is a prepared release fact, separate from audio and subtitle languages.",
  "release_name.use_season_episode":
    "Yes requests season/episode naming when that evidence is available; No uses a known daily date. Auto restores the derived naming choice. This does not invent missing episode evidence.",
  "release_name.three_d":
    "Yes establishes effective 3D; No clears 3D and suppresses effective stereoscopic subtype annotations. Auto restores detection. This does not convert the video.",
  "release_name.no_season":
    "Yes omits both season and episode components from generated names. No leaves them available; Auto removes this saved naming choice. Prepared episode facts remain available.",
  "release_name.no_year":
    "Yes omits the year from generated names. No leaves it available; Auto removes this saved naming choice.",
  "release_name.no_aka":
    "Yes omits the alternate title from generated names. No leaves it available; Auto removes this saved naming choice.",
  "release_name.no_tag":
    "Yes clears the effective release group as well as its name suffix, affecting group checks too. No leaves the group available; Auto restores detection.",
  "release_name.no_episode_title":
    "Yes clears the effective episode title. No leaves it available; Auto restores the derived value.",
  "release_name.no_distributor":
    "Yes clears the effective distributor, including downstream tracker facts. No leaves it available; Auto removes this saved choice.",
  "release_name.no_edition":
    "Yes clears cut/edition and suppresses automatic presentation, feature and release-version annotations. Explicit Presentation, Release features and Release version corrections take precedence. Detected stereoscopic subtype evidence is retained unless 3D is set to No.",
  "release_name.no_dub":
    "Yes suppresses the dubbed marker in the prepared audio naming label. This does not change a track's language or soundtrack origin.",
  "release_name.no_dual":
    "Yes suppresses the dual-audio marker in the prepared audio naming label. This does not remove audio tracks or change their languages.",
  "release_name.dual_audio":
    "Yes forces the dual-audio naming marker, taking precedence over its suppression. This does not establish track-language evidence or add audio tracks.",
  "metadata.distributor":
    "The publisher or distributor used by downstream tracker preparation. Choose a suggestion or enter a custom name or supported numeric tracker ID. An explicit blank clears it.",
  "metadata.original_language":
    "Correct the work's original language. This helps interpret programme audio but does not identify each inspected track's language or prove its soundtrack origin.",
  "metadata.title":
    "The resolved provider title is read-only here. Correct provider IDs and refresh metadata to select the right work.",
  "metadata.alternate_title":
    "Correct the alternate title used by naming and tracker preparation. An explicit blank clears it; Auto restores provider-derived information.",
  "metadata.original_title":
    "The original provider title is read-only here. Correct provider IDs and refresh metadata to select the right work.",
  "metadata.genres":
    "Comma-separated genre names. An empty list explicitly clears the genres; Auto restores provider-derived genres.",
  "metadata.audio_languages":
    "Correct the release-level audio-language list. Search or add custom language names. Clear selections saves an empty list; this does not assign languages to individual tracks.",
  "metadata.subtitle_languages":
    "Correct the release-level subtitle-language list. Search or add custom names. Clear selections saves an empty list; this does not assign languages to individual tracks.",
  "metadata.hardcoded_subtitle_languages":
    "Languages of subtitles burned into the picture. English choices distinguish Full from Forced coverage. Clear selections also clears the hardcoded-subtitle flag; Auto restores detection.",
  "metadata.personal_release":
    "Mark whether this is your personal release in prepared metadata. Auto restores the derived value; tracker policies decide how this fact is used.",
  "metadata.commentary":
    "Correct the release-level commentary flag used by naming and tracker preparation. This does not reclassify individual inspected audio tracks.",
  "metadata.web_dv":
    "Correct the web-sourced Dolby Vision annotation used in release naming. This does not measure or change the video's Dolby Vision metadata.",
  "metadata.stream_optimized":
    "Correct the stream-optimized metadata flag supplied to tracker preparation. This does not remux or optimize the media file.",
  "metadata.anime":
    "Correct whether the prepared release is anime. Downstream naming and tracker checks consume this fact; no media conversion is performed.",
  "metadata.track_languages":
    "Comma-separated language names for this inspected track only. The correction is bound to its track manifest; a changed source mapping requires review of the current track. Auto restores detection.",
  "metadata.track_audio_origins":
    "Identify this programme track as original, dubbed or not yet established using source evidence. Auto restores the derived origin. Language tags alone do not establish origin.",
};
