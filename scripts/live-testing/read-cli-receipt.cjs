const fs = require('node:fs');
const { DatabaseSync } = require('node:sqlite');
const { createHash } = require('node:crypto');

const correctionProperties = {
  identity: {
    TMDBID: 'identity.tmdb', IMDBID: 'identity.imdb', TVDBID: 'identity.tvdb',
    TVmazeID: 'identity.tvmaze', MALID: 'identity.mal',
  },
  releaseName: {
    Category: 'release_name.category', Type: 'release_name.type', Source: 'release_name.source',
    Resolution: 'release_name.resolution', Tag: 'release_name.tag', Service: 'release_name.service',
    Edition: 'release_name.edition', Season: 'release_name.season', Episode: 'release_name.episode',
    EpisodeTitle: 'release_name.episode_title', ManualYear: 'release_name.manual_year',
    ManualDate: 'release_name.manual_date', UseSeasonEpisode: 'release_name.use_season_episode',
    NoSeason: 'release_name.no_season', NoYear: 'release_name.no_year', NoAKA: 'release_name.no_aka',
    NoTag: 'release_name.no_tag', NoEpisodeTitle: 'release_name.no_episode_title',
    NoDistributor: 'release_name.no_distributor', NoEdition: 'release_name.no_edition',
    NoDub: 'release_name.no_dub', NoDual: 'release_name.no_dual', DualAudio: 'release_name.dual_audio',
    Region: 'release_name.region',
  },
  metadata: {
    Distributor: 'metadata.distributor', OriginalLanguage: 'metadata.original_language',
    PersonalRelease: 'metadata.personal_release', Commentary: 'metadata.commentary', WebDV: 'metadata.web_dv',
    StreamOptimized: 'metadata.stream_optimized', Anime: 'metadata.anime', Title: 'metadata.title',
    AlternateTitle: 'metadata.alternate_title', OriginalTitle: 'metadata.original_title',
    Genres: 'metadata.genres', AudioLanguages: 'metadata.audio_languages',
    SubtitleLanguages: 'metadata.subtitle_languages', HardcodedSubs: 'metadata.hardcoded_subs',
    HardcodedSubtitleLanguages: 'metadata.hardcoded_subtitle_languages',
  },
};
const correctionFields = new Set([
  ...Object.values(correctionProperties.identity), ...Object.values(correctionProperties.releaseName),
  ...Object.values(correctionProperties.metadata), 'metadata.track_languages',
]);
const identityResetProperties = new Map([
  ['identity.tmdb', ['identity', 'TMDBID']], ['identity.imdb', ['identity', 'IMDBID']],
  ['identity.tvdb', ['identity', 'TVDBID']], ['identity.tvmaze', ['identity', 'TVmazeID']],
  ['identity.mal', ['identity', 'MALID']], ['release_name.category', ['releaseName', 'Category']],
]);
const readinessMetadataFields = new Set([
  'tmdb_id_only', 'imdb_id_only', 'tvdb_id_only', 'tvmaze_id_only', 'tmdb', 'imdb', 'tvdb', 'tvmaze',
  'tmdb_title', 'imdb_title', 'tvdb_title', 'tvdb_year', 'tvdb_disambiguation', 'tmdb_origin_countries',
  'tmdb_unavailable', 'imdb_unavailable', 'tvdb_unavailable', 'poster', 'title', 'alternate_title',
  'original_title', 'year', 'genres', 'original_language', 'distributor', 'audio_languages',
  'subtitle_languages', 'hardcoded_subs', 'hardcoded_subtitle_languages', 'tmdb_localized_pt_br',
]);
const code = value => typeof value === 'string' && /^[a-z][a-z0-9_]{0,80}$/.test(value) ? value : 'unknown';
const hash = value => createHash('sha256').update(JSON.stringify(value ?? null)).digest('hex');
const trackerID = value => typeof value === 'string' && /^[A-Z0-9]+$/.test(value) ? value : 'UNKNOWN';
const correctionField = value => correctionFields.has(value) ? value : 'unknown';
const readinessKey = value => {
  if (value === 'source' || value === 'type' || value === 'metadata.category' || value === 'tracker_input.no_english_subtitles') return value;
  if (typeof value !== 'string' || !value.startsWith('metadata.')) return 'unknown';
  const fields = value.slice('metadata.'.length).split('.or.');
  return fields.length > 0 && fields.every(field => readinessMetadataFields.has(field)) ? value : 'unknown';
};

function normalizeCorrections(snapshot) {
  const stored = snapshot.corrections?.corrections;
  const fields = [];
  let validIdentifiers = Boolean(stored && typeof stored === 'object' && !Array.isArray(stored));
  for (const [family, properties] of Object.entries(correctionProperties)) {
    const values = stored?.[family];
    if (!values || typeof values !== 'object' || Array.isArray(values)) {
      validIdentifiers = false;
      continue;
    }
    for (const [property, value] of Object.entries(values)) {
      if (family === 'metadata' && property === 'TrackLanguages') continue;
      const field = properties[property];
      if (!field) {
        validIdentifiers = false;
        continue;
      }
      if (value !== null && value !== undefined) fields.push({ field, mode: 'manual', trackIdHash: hash(''), valueHash: hash(value) });
    }
  }
  const identityResetFields = stored?.identityResetFields;
  const seenResetFields = new Set();
  if (identityResetFields !== null && identityResetFields !== undefined) {
    if (!Array.isArray(identityResetFields)) validIdentifiers = false;
    else for (const value of identityResetFields) {
      const property = identityResetProperties.get(value);
      if (!property || seenResetFields.has(value)) {
        validIdentifiers = false;
        continue;
      }
      seenResetFields.add(value);
      if (stored?.[property[0]]?.[property[1]] !== null && stored?.[property[0]]?.[property[1]] !== undefined) {
        validIdentifiers = false;
      }
      fields.push({ field: value, mode: 'automatic', trackIdHash: hash('') });
    }
  }
  const trackLanguages = stored?.metadata?.TrackLanguages;
  if (trackLanguages !== null && trackLanguages !== undefined) {
    if (!Array.isArray(trackLanguages)) validIdentifiers = false;
    else for (const correction of trackLanguages) {
      if (!correction || typeof correction.trackId !== 'string' || correction.trackId.length === 0) {
        validIdentifiers = false;
        continue;
      }
      fields.push({
        field: 'metadata.track_languages', mode: 'manual', trackIdHash: hash(correction.trackId),
        valueHash: hash({ languages: correction.languages, manifestFingerprint: correction.manifestFingerprint }),
      });
    }
  }
  fields.sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  return { fields, validIdentifiers };
}

function inputRevisions(snapshot) {
  return {
    workflow: Number(snapshot.workflow?.revision || 0), release: Number(snapshot.release?.revision || 0),
    factInstructions: Number(snapshot.factInstructions?.revision || 0), inputReadiness: Number(snapshot.inputReadiness?.revision || 0),
    corrections: Number(snapshot.corrections?.revision || 0),
    refs: {
      workflowRelease: Number(snapshot.workflow?.release?.revision || 0),
      workflowFacts: Number(snapshot.workflow?.factInstructions?.revision || 0),
      workflowReadiness: Number(snapshot.workflow?.inputReadiness?.revision || 0),
      releaseFacts: Number(snapshot.release?.factInstructions?.revision || 0),
      readinessFacts: Number(snapshot.inputReadiness?.factInstructions?.revision || 0),
      factCorrections: Number(snapshot.factInstructions?.correctionRevision || 0),
      readinessCorrections: Number(snapshot.inputReadiness?.correctionRevision || 0),
    },
  };
}

function inputRevisionsAgree(snapshot) {
  const revisions = inputRevisions(snapshot);
  const positive = [revisions.workflow, revisions.release, revisions.factInstructions, revisions.inputReadiness, revisions.corrections]
    .every(value => Number.isSafeInteger(value) && value > 0);
  return positive
    && revisions.workflow >= revisions.release && revisions.workflow >= revisions.factInstructions && revisions.workflow >= revisions.inputReadiness
    && revisions.refs.workflowRelease === revisions.release
    && revisions.refs.workflowFacts === revisions.factInstructions
    && revisions.refs.workflowReadiness === revisions.inputReadiness
    && revisions.refs.releaseFacts === revisions.factInstructions
    && revisions.refs.readinessFacts === revisions.factInstructions
    && revisions.refs.factCorrections === revisions.corrections
    && revisions.refs.readinessCorrections === revisions.corrections;
}

function normalizeInput(snapshot) {
  const durable = normalizeCorrections(snapshot);
  let validIdentifiers = durable.validIdentifiers;
  const trackers = (snapshot.inputReadiness?.selectedTrackerIds || []).map(id => {
    const normalized = trackerID(id);
    if (normalized === 'UNKNOWN') validIdentifiers = false;
    return normalized;
  }).sort();
  const readiness = (snapshot.inputReadiness?.fields || []).map(field => {
    const key = readinessKey(field.key);
    const corrected = field.correctionField ? correctionField(field.correctionField) : '';
    const fieldTrackers = (field.trackerIds || []).map(id => trackerID(id)).sort();
    const status = code(field.status);
    const disposition = code(field.disposition);
    if (key === 'unknown' || corrected === 'unknown' || fieldTrackers.includes('UNKNOWN') || status === 'unknown' || disposition === 'unknown') validIdentifiers = false;
    return { key, correctionField: corrected, trackers: fieldTrackers, status, disposition };
  }).sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  return {
    prepared: Boolean(snapshot.release), trackers, corrections: durable.fields, readiness,
    effectiveFactsHash: hash(snapshot.release ? (() => {
      const release = snapshot.release.release || {};
      const naming = release.Naming || {};
      const media = release.Media || {};
      const identity = release.Identity || {};
      return {
        naming: {
          ReleaseName: naming.ReleaseName, Title: naming.Title, AlternateTitle: naming.AlternateTitle,
          OriginalTitle: naming.OriginalTitle, Genres: naming.Genres, Year: naming.Year, Source: naming.Source,
          Type: naming.Type, Resolution: naming.Resolution, Tag: naming.Tag, Group: naming.Group,
          Region: naming.Region, Editions: naming.Editions, Personal: naming.Personal,
        },
        media: {
          AudioLanguages: media.AudioLanguages, SubtitleLanguages: media.SubtitleLanguages,
          HardcodedSubtitleLanguages: media.HardcodedSubtitleLanguages, HardcodedSubs: media.HardcodedSubs,
          OriginalLanguage: media.OriginalLanguage, Distributor: media.Distributor, Commentary: media.Commentary,
          WebDV: media.WebDV, StreamOptimized: media.StreamOptimized, Anime: media.Anime,
          Tracks: (media.Tracks || []).map(track => ({ ID: track.ID, Kind: track.Kind, Languages: track.Languages, LanguageProvenance: track.LanguageProvenance, Commentary: track.Commentary })),
        },
        identity: {
          TMDBID: identity.TMDBID, IMDBID: identity.IMDBID, TVDBID: identity.TVDBID,
          TVmazeID: identity.TVmazeID, MALID: identity.MALID, Category: identity.Category,
          Provenance: identity.Provenance, Overrides: identity.Overrides, Conflict: identity.Conflict,
        },
      };
    })() : null),
    validIdentifiers,
  };
}

function compareInputSnapshots(baselineSnapshot, cliSnapshot) {
  const baseline = normalizeInput(baselineSnapshot);
  const cli = normalizeInput(cliSnapshot);
  const changedFields = Object.keys(baseline).filter(key => JSON.stringify(baseline[key]) !== JSON.stringify(cli[key]));
  const validIdentifiers = baseline.validIdentifiers && cli.validIdentifiers;
  const validLocalRevisions = inputRevisionsAgree(baselineSnapshot) && inputRevisionsAgree(cliSnapshot);
  return {
    baseline, cli, changedFields, validIdentifiers, validLocalRevisions,
    matches: validIdentifiers && validLocalRevisions && changedFields.length === 0,
    revisions: { baseline: inputRevisions(baselineSnapshot), cli: inputRevisions(cliSnapshot) },
  };
}

// All inputs and the richer output stay in the private run. Never print database rows.
const args = process.argv.slice(2);
if (args[0] === '--compare-input-fixture') {
  const comparison = compareInputSnapshots(JSON.parse(fs.readFileSync(args[1], 'utf8')), JSON.parse(fs.readFileSync(args[2], 'utf8')));
  fs.writeFileSync(args[3], JSON.stringify(comparison, null, 2));
  process.exit(0);
}
const [databasePath, apiSnapshotPath, outputPath, expectedWorkflowId, mode = 'full'] = args;
if (!['full', 'input'].includes(mode)) throw new Error('cli_receipt_mode_invalid');
const db = new DatabaseSync(databasePath, { readOnly: true });
try {
  const rows = db.prepare("SELECT state_json FROM release_workflow_states WHERE owner_id = 'cli' ORDER BY updated_at DESC").all();
  const states = rows.map(row => JSON.parse(Buffer.from(row.state_json).toString('utf8')));
  const state = mode === 'input' ? states.find(item => item.Workflow?.inputReadiness) : states.find(item => item.Composite);
  if (!state) throw new Error(mode === 'input' ? 'cli_input_snapshot_missing' : 'cli_composite_snapshot_missing');
  const api = JSON.parse(fs.readFileSync(apiSnapshotPath, 'utf8'));
  if (!expectedWorkflowId || api.workflow?.id !== expectedWorkflowId) throw new Error('cli_baseline_workflow_mismatch');
  const current = { workflow: state.Workflow };
  for (const [field, collection, ref] of [['release', 'Releases', 'release'], ['selection', 'Selections', 'selection'], ['projections', 'Projections', 'trackerProjections'], ['preflight', 'Preflights', 'trackerPreflight'], ['dupes', 'Dupes', 'dupes']]) {
    current[field] = state[collection]?.[state.Workflow[ref]?.id];
  }
  current.inputReadiness = state.InputReadiness?.[state.Workflow.inputReadiness?.id];
  current.factInstructions = state.FactInstructions?.[state.Workflow.factInstructions?.id];
  current.corrections = state.Corrections;
  const normalizeFull = snapshot => ({
    prepared: Boolean(snapshot.release),
    trackers: (snapshot.selection?.trackerIds || []).map(id => /^[A-Z0-9]+$/.test(id) ? id : 'UNKNOWN'),
    projections: (snapshot.projections?.projections || []).map(item => ({
      trackerId: /^[A-Z0-9]+$/.test(item.trackerId) ? item.trackerId : 'UNKNOWN',
      reviewedNameHash: hash(item.uploadReleaseName), taxonomyHash: hash(item.taxonomy),
      providerIdentityHash: hash(item.providerIds), duplicateCriteriaHash: hash(item.duplicateCriteria), duplicateTargetHash: hash(item.duplicateTarget),
      duplicatePolicyHash: hash(item.duplicatePolicyId), readiness: code(item.readiness),
      failureCodes: (item.failures || []).map(failure => code(failure.failure?.code)).sort(),
    })).sort((a, b) => a.trackerId.localeCompare(b.trackerId)),
    duplicates: (snapshot.dupes?.results || []).map(item => ({
      trackerId: /^[A-Z0-9]+$/.test(item.trackerId) ? item.trackerId : 'UNKNOWN',
      decision: code(item.decision), status: code(item.status),
      failureCodes: (item.failures || []).map(failure => code(failure.failure?.code)).sort(),
    })).sort((a, b) => a.trackerId.localeCompare(b.trackerId)),
  });
  const inputComparison = mode === 'input' ? compareInputSnapshots(api, current) : null;
  const baseline = inputComparison?.baseline || normalizeFull(api);
  const cli = inputComparison?.cli || normalizeFull(current);
  const changedFields = inputComparison?.changedFields || Object.keys(baseline).filter(key => JSON.stringify(baseline[key]) !== JSON.stringify(cli[key]));
  const forbiddenEffects = db.prepare("SELECT COUNT(*) AS count FROM release_workflow_effects WHERE kind IN ('tracker_submission', 'client_injection')").get().count;
  const excludedRefs = ['trackerProjections', 'trackerPreflight', 'dupes', 'media', 'descriptions', 'dryRun', 'uploadResult'].filter(field => state.Workflow[field]);
  const safeIntent = mode === 'input'
    ? Boolean(state.Workflow.inputReadiness) && excludedRefs.length === 0
    : state.Composite.goal === 'dry_run' && state.Composite.intent?.noSeed === true;
  const sufficientEvidence = mode === 'input'
    ? baseline.prepared && cli.prepared && baseline.trackers.length > 0 && cli.trackers.length > 0 && baseline.readiness.length > 0 && cli.readiness.length > 0
    : baseline.prepared && cli.prepared && baseline.projections.length > 0 && cli.projections.length > 0 && baseline.duplicates.length > 0 && cli.duplicates.length > 0;
  const invalidInputEvidence = mode === 'input' && (!inputComparison.validIdentifiers || !inputComparison.validLocalRevisions);
  const changedInputEvidence = mode === 'input' && changedFields.length > 0;
  const status = !safeIntent || forbiddenEffects !== 0 || invalidInputEvidence || changedInputEvidence ? 'fail' : sufficientEvidence && changedFields.length === 0 ? 'pass' : 'inconclusive';
  fs.writeFileSync(outputPath, JSON.stringify({
    version: 1, status, reason: status === 'fail' ? 'cli_safety_contract_failed' : status === 'pass' ? 'shared_decision_observations_match' : 'shared_decision_evidence_requires_review',
    mode, safeIntent, executionMode: mode === 'input' ? 'normal' : code(state.Composite.intent?.executionMode), forbiddenEffects, excludedRefs,
    sufficientEvidence, changedFields, baseline, cli,
    inputValidation: mode === 'input' ? {
      validIdentifiers: inputComparison.validIdentifiers, validLocalRevisions: inputComparison.validLocalRevisions,
      revisions: inputComparison.revisions,
    } : undefined,
    limitation: 'Live evidence may change between observations. Name hashes compare equality without disclosing media identity; differing results require review.',
  }, null, 2));
} finally { db.close(); }
