# Duplicate policy evidence matrix

Source snapshots are the saved files under `docs/trackerdata`. Their capture
dates/freshness are unknown, so subjective, age/seed, screenshot, approval, and
staff-discretion rules remain `manual_review` unless an adapter returns all
required structured evidence.

Search completion is separate from work identity. Public `complete=true`
requires both exhausted enumeration and a provider- or tracker-group-bound
query. Title and release-name searches remain incomplete even when their
returned page or array is exhausted.

## Search coverage audit

| Registered adapter(s) | Work binding | Enumeration contract | Effective completion |
| --- | --- | --- | --- |
| A4K, ACM, AITHER, BLU, CBR, DP, EMUW, FRIKI, HHD, IHD, ITT, LCD, LDU, LST, LT, LUME, MNS, OE, OTW, PT, PTT, R4E, RAS, RF, RHD, SAM, SHRI, SP, STC, TIK, TLZ, TOS, TTR, ULCX, UTP, YUS, ZNTH | exact TMDB ID plus broad movie/TV category; season only for TV scope | all normal and pending pages; overlap deduped by torrent ID | yes after every endpoint and page is exhausted |
| AZ, CZ, PHD | tracker media group | every advertised torrent-list page, deduped by torrent ID/link | yes after all pages are exhausted |
| ANT | exact TMDB/IMDb ID | bounded Newznab limit/offset traversal with total/offset checks | yes when pagination proves exhaustion |
| AR | title/year | every consistent advertised page up to the safety bound | no |
| ASC | exact IMDb ID, or anime title fallback | one response; pagination semantics unknown | no |
| BHD | exact TMDB/IMDb ID plus broad movie/TV category and TV season scope | all advertised pages up to the safety bound | yes when pagination proves exhaustion |
| BHDTV | none | manual check | not run |
| BJS | exact IMDb group page | one group page; completeness unknown | no |
| BTN | tracker group, exact TVDB provider ID, or title fallback; daily searches also bind date/category | ordinary `getTorrents` searches use 100-row pages and zero-based returned-row offsets until the stable `results` total is reached; daily searches are one-shot | tracker-group/provider searches after total exhaustion; title fallback remains non-authoritative; daily is incomplete when one request does not reach the total |
| BT | exact IMDb group, or anime title fallback | group-page discovery; search pagination and partial group-fetch semantics unknown | no |
| CZT | broad title | returned array exhausted | no |
| DC | exact IMDb ID | complete provider result array | yes |
| FF | exact IMDb group, or anime title fallback | group-page discovery; search pagination unknown | no |
| FL | exact IMDb ID, or title fallback; all categories | one page; pagination semantics unknown | no |
| GPW | exact IMDb ID | complete provider result array | yes |
| HDB | exact IMDb/TVDB ID, or title fallback | bounded page traversal with short-page/repeat checks | provider search only, after exhaustion |
| HDS | exact IMDb ID; all categories | next-page traversal up to the safety bound | yes after the final page |
| HDT | exact IMDb ID, or title fallback; all categories | one page; pagination semantics unknown | no |
| IS | exact IMDb ID for movies, or broad title fallback | one page; pagination semantics unknown | no |
| MTV | exact IMDb/TMDB/TVDB ID, or title fallback | bounded Newznab offset traversal | provider search only, after exhaustion |
| NBL | exact TVmaze/IMDb ID, or title fallback | validated page/count/total traversal | provider search only, after exhaustion |
| PTP | exact tracker IMDb group | complete group torrent collection | yes |
| PTS | exact IMDb ID | one page; pagination semantics unknown | no |
| RTF | exact IMDb ID, or title/year fallback | complete returned array | provider search only |
| SPD | exact IMDb ID, or title fallback | complete returned array | provider search only |
| THR | exact IMDb ID | next-page traversal up to the safety bound | yes after the final page |
| TL | broad title | returned array exhausted | no |
| TVC | none | manual check for supported content | not run |

| Tracker/reference | Saved source | Candidate evidence | Complete search scope | Automated boundary |
| --- | --- | --- | --- | --- |
| Aither | `aither/*slots*`, `*trumping*`, `*rules*` | Unit3D title, type, resolution, flags, size, files, internal/trumpable | work/category, season plus packs, all pages | Exact identity and proven distinct slots; ambiguous quality/trump rules require review |
| ANT | `ant/Dupes & Trumping*.htm` | title/files/size/resolution plus exact `DV` and generic tracker `HDR10` flags | TMDB/IMDb work search; bounded Newznab limit/offset traversal with completion evidence | DV/generic-HDR limitations stay partial; HDR10+ vs generic HDR is insufficient evidence |
| AR | `ar/Uploading Guidelines __ AlphaRatio.htm` | browse name, size, file count, group/torrent IDs; source/resolution/codec/group title facts are partial; no fabricated file list | title/year query, every consistent advertised page up to policy bound | Exact identity and the evidenced source/resolution/codec/group tuple; missing tuple facts are insufficient and structured/title contradictions require review |
| AvistaZ | `az network/Upload Rules - AvistaZ.htm` | each row's native type/flags, size, and release name | tracker media group, every advertised page | Source priority/subjective cases remain review |
| CinemaZ | `az network/Upload Rules - CinemaZ.htm` | each row's native type/flags, size, and release name | tracker media group, every advertised page | Naming consumes structured media HDR; moderation/subjective rules remain review |
| BHD | `bhd/Torrent Search _ API*.htm`, `Upload Rules*.htm` | independent `dv`, `hdr10`, `hdr10+`, `hlg`, category/type/source/size | work/category, all advertised pages up to policy bound | Independent HDR slots, exact identity, and scoped 1080p size variance; missing fields require review |
| BTN | `btn/Upload Rules __ BroadcasTheNet.htm`, `btn/Release Name __ BroadcasTheNet.htm`, and sanitized inline API contract tests | category, source, codec, container, coarse/fine resolution, origin, group/internal status, provider IDs, size, HDR/DV presence, and reservation time | tracker group or TVDB ID, stable `results` total, all ordinary pages; title fallback and incomplete daily responses cannot prove capacity | `standalone/btn/duplicate/v1`, evidence ID `btn-upload-rules-sha256-ceffed934da279bf7bd3dc04ab86cbc13bbcca9618ad2c415369e059df603f6f`; objective slots, precedence, and complete-set capacities only |
| DP | `dp/` saved naming/rule snapshots | Unit3D structured fields | work/category, season plus packs, all pages | Proven slot differences only; unresolved rule evidence requires review |
| HDB | `hdb/` saved rules | existing HDB response only | exact provider query or incomplete title fallback | General fallback retains unresolved candidates; proven distinctions may coexist |
| HHD | `hhd/` saved naming/rule snapshots | Unit3D title/type/resolution/HDR; provider/codec unavailable in current search response | work/category, season plus packs, all pages | Missing provider/codec evidence prevents automatic same-slot decisions |
| LST | `lst/` saved rules | Unit3D structured fields | work/category, season plus packs, all pages | Exact/proven slot decisions; otherwise review |
| LUME | `lume/` saved naming/rule snapshots | Unit3D title/type/resolution/HDR; provider unavailable | work/category, season plus packs, all pages | Directional DV/HDR compatibility and encode-only 20% size rule; missing provider evidence requires review |
| MTV | `mtv/` saved rules plus sanitized Torznab fixture | title, count, size, proven attrs; no fabricated file list | work identity, Newznab offset/total until complete or bound | Title-derived HDR is partial; missing/ambiguous HDR/files is manual; pack direction uses the general containment rule |
| NBL | `nbl/Rules _ Uploading _ Uploading Overview __ Nebulance.io.htm` plus current search response shape | structured `tags` with independent `hdr`/`dovi`; title fallback remains partial | zero-based `page`/`per_page` traversal validated against `current_page`, `total_pages`, `count`, and `total_results` | SDR/HDR/DV/DV+HDR slots; HDR10 and HDR10+ collapse to NBL's generic `hdr` slot |
| OTW | `otw/` saved rules | Unit3D structured fields | work/category, season plus packs, all pages | Cross-resolution/type results are fetched; unresolved precedence is review |
| PTP | `ptp/Upload Rules __ PassThePopcorn.htm`, `ptp/PTP API Documentation __ PassThePopcorn.htm` | exact-group `Quality`, `Source`, `Container`, `Codec`, `Resolution`, `Size`, `ReleaseName`, `ReleaseGroup`, and `RemasterTitle`; locally observed `RemasterTitle` is authoritative HDR/DV metadata and absent HDR markers mean SDR | exact IMDb group, complete `Torrents` collection, one response | Objective structural slots plus collection-level SD/HD/UHD encode capacity and minimum size separation; size never establishes quality, and replacement remains review without authoritative trumpability |
| RF | `rf/` saved rules | Unit3D structured fields | work/category, season plus packs, all pages | Proven slots only; subjective cases review |
| RMC | `rmc/` reference | no registered implementation | none | Reference rules are movie-only; enforce strict movie-only when an implementation is registered |
| RTF | `rtf/Rules _ Retroflix.htm` | returned name, size, file names/count and fixture-proven structured technical fields; remaining title facts are partial | confirmed IMDb array is complete; title/year fallback remains explicitly incomplete | Ordered resolution and remux direction plus PAL/NTSC full-DVD coexistence; container/aspect/upscale ambiguity remains review |
| SP | `sp/` saved rules | Unit3D structured fields | work/category, season plus packs, all pages | Proven slots only; unresolved cases review |
| ULCX | `ulcx/` saved rules | Unit3D title/type/resolution/HDR/size | work/category, season plus packs, all pages | 1080p-only absolute 20% size rule plus proven slots |
| YUS | `yus/` saved naming/rule snapshots | Unit3D structured fields | work/category, season plus packs, all pages | Proven slots only; unresolved cases review |

Fixture contracts:

- `impl/standalone/bhd/testdata/search_hdr_variants.json`: DV-only, HDR10,
  HDR10+, DV+HDR10, DV+HDR10+, HLG, SDR, absent, and malformed fields.
- `impl/standalone/ant/testdata/search_flags_variants.json`: DV, generic HDR,
  combined, empty, absent, and unknown flags.
- `impl/unit3d/testdata/search_flags_variants.json`: present, empty, and absent
  Unit3D flags.
- `impl/standalone/mtv/testdata/torznab_attributes.xml`: sanitized Newznab
  pagination and Torznab attributes, including combined DV/HDR.
- `impl/standalone/ar/testdata/browse_page_1.json` and
  `browse_page_2.json`: two advertised pages, terminal empty page, file counts,
  and title-fact variants without file-list invention.
- `impl/standalone/ptp/testdata/imdb_group.json` and
  `torrent_group_variants.json`: exact group resolution and all returned SD,
  HD, UHD, encode, WEB-DL, remux, DVD, HDR/DV, cut, and remaster variants.
  Every fixture torrent retains its returned size and technical fields.
  `RemasterTitle` is the complete tracker HDR/DV field; no HDR marker means
  SDR. The proven response has no tracker trumpable field or complete file
  list, so neither is inferred.
- `impl/standalone/rtf/testdata/search_variants.json`: unpaged IMDb array,
  file-count agreement/disagreement, size absence, upgrade/remux/DVD, and
  ambiguous title variants.
- BTN uses sanitized inline `httptest` responses for string/number `results`,
  zero-based offsets, duplicate IDs, malformed rows, partial failures, optional
  field presence, one-shot daily searches, unsorted reservation timestamps, and
  empty results. No live credentials or tracker payloads are retained.

Expected automatic relation boundaries:

| Tracker/rule | Required facts | Relation | Missing/ambiguous fallback |
| --- | --- | --- | --- |
| AR duplicate tuple | source, resolution, codec, group | different tuple `coexists`; equal tuple `same_slot` | missing `insufficient_evidence`; contradiction `manual_review` |
| RTF resolution/remux | ordered resolution plus media kind | directional `proposed_trumps` / `existing_preferred` | unsupported direction stays review |
| RTF DVD standard | full DVD, DVD source, PAL/NTSC | `coexists` | PAL/NTSC without full-DVD evidence stays review |
| PTP structural slot | media kind, resolution, authoritative cut when present, supported HDR distinction | different structural slot `coexists`; equal slot `same_slot` | missing facts `insufficient_evidence` |
| PTP SD/HD WEB-DL | WEB-DL media kind and ordered resolution class | directional proposed/existing preference | incomplete class evidence `insufficient_evidence` |
| PTP encode capacity | complete exact-group set, encode family/cut/technical facts, size, and family threshold | `coexists` only while capacity remains and every required pair meets 20%/40% separation | full capacity or insufficient separation `manual_review`; missing/partial facts `insufficient_evidence` |
| BTN objective slots | authoritative source, concrete resolution, codec, release origin, or PAL/NTSC region as required by the matched rule | Scene/P2P, WEB H.265/H.264, Blu-ray/DVD, and PAL/NTSC may `coexist`; H.264 directionally supersedes Xvid for equal source/resolution | missing or coarse-overlap facts remain `insufficient_evidence`; mixed origin remains `manual_review` |
| BTN season-pack capacity | complete tracker-group/TVDB search plus pack, season, source, resolution, origin, and provider where WEB requires it | Scene capacity 1, P2P capacity 2, WEB 720p/1080p capacity 2, WEB SD/2160p capacity 1 | full capacity `manual_review`; incomplete search or missing provider/season facts `insufficient_evidence` |

## BTN API contract decisions

- Page size remains 100. One operator-observed 150-row request proves acceptance
  for that request only; no stable maximum or unclamped 150-row contract was
  retained. Pagination supplies correctness without relying on it.
- `results` accepts a non-negative JSON number or decimal string. Ordinary
  completion requires a consistent total equal to unique returned torrent IDs.
- Offsets are zero-based and advance by returned row count. Ordering is not
  assumed; reservation checks enumerate the complete bounded result and select
  the newest matching valid `Time`.
- Daily queries retain `Episode` plus `YYYY.MM.DD%` filtering and issue one
  request. A larger reported total is explicit incomplete evidence.
- BTN `Source` maps to `DupeEntry.Source`, not `Type`. `Category`, `Codec`,
  `Container`, `Resolution`, `Origin`, `GroupName`, provider IDs, size, HDR/DV,
  and `Time` retain independent typed meanings.

## BTN rule ledger

| Rule ID | Saved evidence SHA-256 | Required facts | Disposition and phase |
| --- | --- | --- | --- |
| `btn.name.scene` | Release Name `1E51ACEACF2DA47D4795DA388CCEA1787795FDC738C447FAB3691CCAADD9D02A` | verified Scene state and Scene/release name | preserve automatically, phase 5 |
| `btn.name.daily-year-sd` | Release Name `1E51ACEACF2DA47D4795DA388CCEA1787795FDC738C447FAB3691CCAADD9D02A` | daily date, series title/year, concrete resolution | normalize automatically, phase 5 |
| `btn.name.group-anime` | Release Name `1E51ACEACF2DA47D4795DA388CCEA1787795FDC738C447FAB3691CCAADD9D02A` | file groups or anime single-episode filename | mixed packs use `BTN`; anime exception stays narrow, phase 5 |
| `btn.name.folder` | Release Name `1E51ACEACF2DA47D4795DA388CCEA1787795FDC738C447FAB3691CCAADD9D02A` | separate tracker folder-name contract | deferred; current preparation has no independent folder output, phase 5 |
| `btn.special.manual` | TV Movies/Specials `F1173BF2848C267A121D879E01147DFF488F9D37EF518535A539C21D9D9F4E5C` | explicit BTN series/network/organization fields | deferred; shared questionnaire cannot express the path without broad UI/API work, phase 5 |
| `btn.package.shape` | Upload Rules `CEFFED934DA279BF7BD3DC04AB86CBC13BBCCA9618AD2C415369E059DF603F6F` | complete package files, folders, seasons, episode range | proven violations strict; missing evidence advisory, phase 4 |
| `btn.package.uniformity` | Upload Rules `CEFFED934DA279BF7BD3DC04AB86CBC13BBCCA9618AD2C415369E059DF603F6F` | complete per-file media and group facts | mixed evidence waivable/manual, phase 4 |
| `btn.media.taxonomy` | Upload Rules `CEFFED934DA279BF7BD3DC04AB86CBC13BBCCA9618AD2C415369E059DF603F6F` | container, source, codec, resolution, disc/Scene state | proven unsupported media, DVD remux, or Scene DVD image strict, phase 4 |
| `btn.dupe.objective` | Upload Rules `CEFFED934DA279BF7BD3DC04AB86CBC13BBCCA9618AD2C415369E059DF603F6F` | source, resolution, codec, origin, region | automatic coexistence/precedence through policy v1, phase 3 |
| `btn.dupe.capacity` | Upload Rules `CEFFED934DA279BF7BD3DC04AB86CBC13BBCCA9618AD2C415369E059DF603F6F` | complete work search, pack, season, source, resolution, origin, provider | automatic capacity accounting; full/manual and incomplete/insufficient, phase 3 |
| `btn.dupe.internal-language-pilot` | Upload Rules `CEFFED934DA279BF7BD3DC04AB86CBC13BBCCA9618AD2C415369E059DF603F6F` | authoritative target/candidate internal, language, and pilot facts | deferred; current shared typed evidence cannot prove both operands, phase 3 |
| `btn.dupe.subjective` | Upload Rules `CEFFED934DA279BF7BD3DC04AB86CBC13BBCCA9618AD2C415369E059DF603F6F` | quality, defect, request, account, or staff state | manual/advisory only, phases 3-4 |
| `btn.reservation` | Upload Rules `CEFFED934DA279BF7BD3DC04AB86CBC13BBCCA9618AD2C415369E059DF603F6F` | complete paged internal-group episode set, release name, valid `Time` | newest matching row blocks before two hours; unavailable evidence is distinct, phase 6 |
| `btn.claims` | Upload Rules `CEFFED934DA279BF7BD3DC04AB86CBC13BBCCA9618AD2C415369E059DF603F6F` | claimed-thread cache and air time | existing 48-hour TTL/grace and stale-cache failure fallback preserved, phase 6 |

Every registered tracker shares `general/duplicate/v4`: exact filename or
release identity blocks, explicit content/slot differences may coexist,
same-season pack containment is directional, and unresolved candidates fall
back to actionable `same_slot`. Tracker overlays add only evidence-backed
slot, precedence, size, or set rules. Missing overlay evidence never replaces
the general reason with a synthetic compatibility/manual fallback.
