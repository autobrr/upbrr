// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package architecturepolicy enforces lasting ownership boundaries for the
// canonical runtime and prepared-release architecture.
package architecturepolicy

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Violation is one source location that crosses a protected architecture boundary.
type Violation struct {
	File    string
	Line    int
	Column  int
	Message string
}

var releasePageDirectories = map[string]struct{}{
	"bluray_candidates":   {},
	"description_builder": {},
	"dupe_check":          {},
	"input":               {},
	"menu_images":         {},
	"playlist_selection":  {},
	"screenshots":         {},
	"tracker_data":        {},
	"tracker_upload":      {},
	"upload_images":       {},
}

var messageRecoveryPattern = regexp.MustCompile(`(?i)(error|message)[A-Za-z0-9_?.]*(?:\.toLowerCase\(\))?\.(includes|startsWith|match)\(`)

var frontendBDMVPathPattern = regexp.MustCompile(`(?i)(\$\{[^}]+\}[^\n]*[\\/]BDMV|[+][^\n]*["'\x60][\\/]?BDMV|BDMV[^\n]*\.slice\()`)

var preparationIntentTrackersPattern = regexp.MustCompile(`(?s)export\s+type\s+PreparationIntent\s*=.*?\btrackers\b.*?};`)

var genericWorkflowTrackerDispatchPattern = regexp.MustCompile(`(?i)EqualFold\([^,\n]*tracker[^,\n]*,\s*"[a-z0-9]+"\)`)

var sourcePathArtifactLookupPattern = regexp.MustCompile(`\b(?:Load|Get|Find)(?:Media|Description|Dupe|UploadPlan)?BySourcePath\b`)

var optionalWorkflowPortPattern = regexp.MustCompile(`\bworkflow\s*\?:`)

var apiV1RouteRequestTypePattern = regexp.MustCompile(`^apiV1[A-Za-z0-9]*Request$`)

var publicationCommandNames = map[string]struct{}{
	"CompositeUploadCommand":       {},
	"PublishDescriptionsCommand":   {},
	"PublishDupeAssessmentCommand": {},
	"PublishMediaArtifactsCommand": {},
	"PublishPreflightCommand":      {},
	"PublishProjectionSetCommand":  {},
	"PublishUploadPlanCommand":     {},
	"SetTrackerContextCommand":     {},
}

var callerVisibleUploadPlanMarkers = []string{
	"BuildReleaseWorkflowUploadPlan",
	"ExecuteReleaseWorkflowUploadPlan",
	"BuildUploadPlanCommand",
	"ExecuteUploadPlanCommand",
	"UploadPlanRef",
}

var removedBrowserWorkflowRoutes = []string{
	`/api/app/DetectDiscType`,
	`/api/app/FetchMetadata`,
	`/api/app/ResetMetadata`,
	`/api/app/SelectBlurayCandidate`,
	`/api/app/FetchPreparation`,
	`/api/app/FetchTrackerDryRun`,
	`/api/app/FetchDescriptionBuilder`,
	`/api/app/GenerateScreenshots`,
	`/api/app/CaptureDVDMenus`,
	`/api/app/StartDupeCheck`,
	`/api/app/ReviewTrackerUpload`,
	`/api/app/StartReviewedTrackerUpload`,
	`/api/app/RetryFailedTrackerUpload`,
	`/api/app/CancelDupeCheck`,
	`/api/app/CancelTrackerUpload`,
	`/api/app/GetDupeCheckSnapshot`,
	`/api/app/GetTrackerUploadSnapshot`,
	`/api/app/ListJobs`,
}

var removedFrontendWorkflowCommands = []string{
	`"DetectDiscType"`,
	`"FetchMetadata"`,
	`"ResetMetadata"`,
	`"SelectBlurayCandidate"`,
	`"FetchPreparation"`,
	`"FetchTrackerDryRun"`,
	`"FetchDescriptionBuilder"`,
	`"GenerateScreenshots"`,
	`"CaptureDVDMenus"`,
	`"StartDupeCheck"`,
	`"ReviewTrackerUpload"`,
	`"StartReviewedTrackerUpload"`,
	`"RetryFailedTrackerUpload"`,
}

var workflowInterfaceMarkers = []string{"Capability", "Module", "Operation", "Runner", "Service", "Workflow"}

var preparedStateFields = map[string]struct{}{
	"BlockedTrackers":             {},
	"ClientTorrentPath":           {},
	"ClientOverrides":             {},
	"CrossSeedTorrents":           {},
	"DescriptionOverride":         {},
	"DescriptionGroups":           {},
	"ImageHostOverrides":          {},
	"IgnoreDupesFor":              {},
	"Mode":                        {},
	"Options":                     {},
	"QuestionnaireAnswers":        {},
	"RuleAuthorizations":          {},
	"ScreenshotOverrides":         {},
	"TorrentOverrides":            {},
	"TorrentPath":                 {},
	"TrackerConfigOverrides":      {},
	"TrackerQuestionnaireAnswers": {},
	"TrackerRuleFailures":         {},
	"Trackers":                    {},
	"TrackersRemove":              {},
	"TrackerSiteOverrides":        {},
}

var hostActivationFunctions = map[string]struct{}{
	"applyConfig":            {},
	"buildAndInstallRuntime": {},
	"saveAndApplyConfig":     {},
}

var trackerUploadNamingFunctions = map[string]struct{}{
	"applyBTNNameMapping": {},
	"addNoGroup":          {},
	"buildACMName":        {},
	"buildName":           {},
	"buildUnit3DName":     {},
	"cleanName":           {},
	"editName":            {},
	"normalizeName":       {},
	"resolveARName":       {},
	"resolveName":         {},
	"resolveSearchName":   {},
	"resolveSearchTitle":  {},
	"resolveSubject":      {},
	"resolveUploadTitle":  {},
	"resolveUploadName":   {},
}

var trackerUploadResponsibilityPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{name: "auth", pattern: regexp.MustCompile(`(?:Auth|Login|Session|Cookie|TOTP|CSRF|Passkey|UploadToken|AuthToken)`)},
	{
		name: "taxonomy",
		pattern: regexp.MustCompile(
			`^(?:categoryOf|(?:resolve|map|infer|detect|normalize|has|is).*(?:Category|TypeID|UploadType|Resolution|Codec|Container|Source|Quality|Language|Subtitle|Audio|Tags?|Flags?|Region|Edition|RipType|Adult|Romanian|SD|TV))$`,
		),
	},
	{name: "description", pattern: regexp.MustCompile(`(?:Description|Screenshots?)`)},
	{name: "media", pattern: regexp.MustCompile(`(?:MediaInfo|BDInfo|NFO|Duration|Runtime|Autofill)`)},
	{name: "questionnaire", pattern: regexp.MustCompile(`(?:Questionnaire|Answers?|Schema)`)},
	{
		name:    "validation",
		pattern: regexp.MustCompile(`^validate(?:Payload|Fields|Eligibility|.*PayloadMetadata|Constructibility|Requirements)`),
	},
}

var unit3DCallbackFiles = map[string]string{
	"ApplyAdditionalPayload": "payload.go",
	"BuildDescription":       "description.go",
	"BuildName":              "name.go",
	"FinalizeDescription":    "description.go",
	"ResolveCategoryID":      "taxonomy.go",
	"ResolveKeywords":        "taxonomy.go",
	"ResolveResolutionID":    "taxonomy.go",
	"ResolveTypeID":          "taxonomy.go",
}

var forbiddenTrackerImportPrefixes = []string{
	"github.com/autobrr/upbrr/cmd/upbrr",
	"github.com/autobrr/upbrr/internal/core",
	"github.com/autobrr/upbrr/internal/releaseworkflow",
	"github.com/autobrr/upbrr/internal/webserver",
}

var principalTrackerPayloadFields = map[string]struct{}{
	"name":         {},
	"release_name": {},
	"scenename":    {},
	"title":        {},
	"torrent_name": {},
}

// CheckRepository scans architecture-sensitive source without following generated,
// dependency, build-output, or VCS directories. It returns violations sorted by
// file, line, then column.
func CheckRepository(root string) ([]Violation, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root: %w", err)
	}
	var violations []Violation
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && ignoredDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("architecture policy: resolve relative path %q: %w", path, err)
		}
		relative = filepath.ToSlash(relative)
		var found []Violation
		switch filepath.Ext(path) {
		case ".go":
			if strings.HasSuffix(path, ".generated.go") {
				return nil
			}
			found, err = checkGoFile(path, relative)
		case ".ts", ".tsx":
			found, err = checkFrontendFile(path, relative)
		default:
			return nil
		}
		if err != nil {
			return err
		}
		violations = append(violations, found...)
		guardViolations, err := checkWorkflowGuards(path, relative)
		if err != nil {
			return err
		}
		violations = append(violations, guardViolations...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan repository: %w", err)
	}
	slices.SortFunc(violations, func(left, right Violation) int {
		if result := strings.Compare(left.File, right.File); result != 0 {
			return result
		}
		if left.Line != right.Line {
			return left.Line - right.Line
		}
		return left.Column - right.Column
	})
	return violations, nil
}

// checkWorkflowGuards protects the final transport and exact-plan seams from
// reintroducing legacy orchestration after the workflow migration.
func checkWorkflowGuards(path string, relative string) ([]Violation, error) {
	if strings.HasSuffix(relative, "_test.go") || strings.HasSuffix(relative, ".test.ts") || strings.HasSuffix(relative, ".test.tsx") {
		return nil, nil
	}
	relevant := (strings.HasPrefix(relative, "internal/webserver/") && strings.HasSuffix(relative, ".go")) ||
		(strings.HasPrefix(relative, "internal/releaseworkflow/") && strings.HasSuffix(relative, ".go")) ||
		(strings.HasPrefix(relative, "webui/src/api/") && (strings.HasSuffix(relative, ".ts") || strings.HasSuffix(relative, ".tsx"))) ||
		relative == "webui/src/releaseSession/index.tsx" ||
		(strings.HasPrefix(relative, "pkg/api/") && strings.HasSuffix(relative, ".go")) ||
		(strings.HasPrefix(relative, "cmd/upbrr/") && strings.HasSuffix(relative, ".go")) ||
		relative == "internal/trackers/workflow_projector.go" ||
		relative == "internal/core/workflow_upload_plan.go" ||
		relative == "internal/releaseworkflow/module.go" ||
		strings.HasPrefix(relative, "internal/core/workflow_")
	if !relevant {
		return nil, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read workflow guard %s: %w", relative, err)
	}
	text := string(content)
	var violations []Violation
	add := func(offset int, message string) {
		line, column := textPosition(text, max(offset, 0))
		violations = append(violations, Violation{
			File:    relative,
			Line:    line,
			Column:  column,
			Message: message,
		})
	}
	if strings.HasPrefix(relative, "cmd/upbrr/") {
		for _, marker := range []string{
			"RunUploadPrepared(",
			"RunAcceptedTrackerDryRun(",
			"FetchMetadataPreview(",
			"BuildUploadReview(",
			"CheckDupes(",
			"GenerateScreenshots(",
			"CaptureDVDMenus(",
			"DetectDiscType(",
			"DiscoverPlaylists(",
			"runCLIUploadOnlyBatch(",
			"runCLIUploadOnlyQueue(",
			"runSiteCheckCLIItem(",
			"runInteractiveCLIPath",
			"handleBDMVPlaylistSelection(",
		} {
			if offset := strings.Index(text, marker); offset >= 0 {
				add(offset, "CLI release orchestration must use in-process release workflow commands")
			}
		}
	}
	if strings.HasPrefix(relative, "internal/webserver/") {
		if strings.HasPrefix(relative, "internal/webserver/jobs/") || relative == "internal/webserver/jobs.go" {
			add(0, "legacy release Job package is removed")
		}
		for _, marker := range removedBrowserWorkflowRoutes {
			if offset := strings.Index(text, marker); offset >= 0 {
				add(offset, "legacy browser workflow route is removed: "+marker)
			}
		}
	}
	if strings.HasPrefix(relative, "internal/releaseworkflow/") {
		for _, marker := range []string{
			"SetTrackerContextCommand",
			"PublishProjectionSetCommand",
			"PublishPreflightCommand",
			"PublishDupeAssessmentCommand",
			"PublishMediaArtifactsCommand",
			"PublishDescriptionsCommand",
			"PublishUploadPlanCommand",
		} {
			if offset := strings.Index(text, marker); offset >= 0 {
				add(offset, "workflow publication inputs must remain package-private: "+marker)
			}
		}
	}
	if strings.HasPrefix(relative, "webui/src/api/") {
		for _, marker := range removedFrontendWorkflowCommands {
			if offset := strings.Index(text, marker); offset >= 0 {
				add(offset, "frontend release orchestration must use releaseWorkflowClient: "+marker)
			}
		}
	}
	if strings.HasPrefix(relative, "cmd/upbrr/") || strings.HasPrefix(relative, "internal/webserver/") ||
		strings.HasPrefix(relative, "pkg/api/") || strings.HasPrefix(relative, "webui/src/api/") ||
		relative == "webui/src/releaseSession/index.tsx" {
		for _, marker := range callerVisibleUploadPlanMarkers {
			if offset := strings.Index(text, marker); offset >= 0 {
				add(offset, "callers cannot build or execute a release upload plan: "+marker)
			}
		}
	}
	switch relative {
	case "internal/trackers/workflow_projector.go":
		if match := genericWorkflowTrackerDispatchPattern.FindStringIndex(text); match != nil {
			add(match[0], "workflow projection must use registry-owned stable tracker dispatch")
		}
		for _, marker := range []string{"BuildUploadReview(", "CreateTorrent(", ".Prepare("} {
			if offset := strings.Index(text, marker); offset >= 0 {
				add(offset, "workflow projection must remain side-effect free: "+marker)
			}
		}
	case "internal/core/workflow_preflight.go":
		if !strings.Contains(text, "trackerauth.IsManagedCapability") {
			add(0, "workflow preflight must gate remote auth with trackerauth.IsManagedCapability")
		}
	case "internal/core/workflow_upload_plan.go":
		if found := strings.Contains(text, "UploadReleaseName: projection.UploadReleaseName"); !found {
			add(0, "upload plan must retain the finalized projection upload name")
		}
	case "internal/releaseworkflow/module.go":
		if found := strings.Contains(text, "prepared.execution.Execute(ctx, trackerIDs)"); !found {
			add(0, "direct upload must execute private preparation returned by the same command")
		}
	case "webui/src/releaseSession/index.tsx":
		for _, marker := range []string{
			"releaseJobs.startDupe(",
			"releaseJobs.startUpload(",
			"releaseJobs.retryUpload(",
		} {
			if offset := strings.Index(text, marker); offset >= 0 {
				add(offset, "release-session workflow cannot start legacy release Jobs")
			}
		}
	}
	if strings.HasPrefix(relative, "internal/core/workflow_") {
		if match := sourcePathArtifactLookupPattern.FindStringIndex(text); match != nil {
			add(match[0], "workflow artifacts cannot use source-path-only lookup")
		}
	}
	return violations, nil
}

func ignoredDirectory(name string) bool {
	switch name {
	case ".git", ".gocache", "dist", "node_modules", "tmp", "vendor":
		return true
	default:
		return false
	}
}

// checkGoFile enforces ownership boundaries that require Go syntax and type-name inspection.
func checkGoFile(path string, relative string) ([]Violation, error) {
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", relative, err)
	}
	var violations []Violation
	hasPrincipalTrackerPayload := false
	hasReviewedUploadName := false
	add := func(position token.Pos, message string) {
		location := files.Position(position)
		violations = append(violations, Violation{
			File:    relative,
			Line:    location.Line,
			Column:  location.Column,
			Message: message,
		})
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		if strings.HasSuffix(relative, "_test.go") {
			return true
		}
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && isTrackerUploadFile(relative) &&
				selector.Sel.Name == "ReviewedUploadName" {
				hasReviewedUploadName = true
			}
			if identifier, ok := call.Fun.(*ast.Ident); ok && isTrackerUploadFile(relative) {
				if _, forbidden := trackerUploadNamingFunctions[identifier.Name]; forbidden {
					add(identifier.Pos(), "tracker upload payloads must consume the reviewed release name; naming algorithms belong in name.go")
				}
			}
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "SearchPathedTorrents" &&
				!strings.HasPrefix(relative, "internal/clientdiscovery/") {
				add(selector.Sel.Pos(), "production torrent-client search belongs to internal/clientdiscovery")
			}
		}
		if field, ok := node.(*ast.KeyValueExpr); ok && isTrackerUploadFile(relative) {
			if key, ok := field.Key.(*ast.BasicLit); ok && key.Kind == token.STRING {
				if _, principal := principalTrackerPayloadFields[strings.Trim(key.Value, `"`+"\x60")]; principal {
					hasPrincipalTrackerPayload = true
				}
			}
		}
		if selector, ok := node.(*ast.SelectorExpr); ok && strings.HasPrefix(relative, "internal/preparedrelease/") &&
			selectedTypeName(selector.X) == "api" && selector.Sel.Name == "InteractionModeUnattended" {
			add(selector.Pos(), "canonical preparation must preserve caller interaction mode")
		}
		if importSpec, ok := node.(*ast.ImportSpec); ok && strings.HasPrefix(relative, "internal/trackers/impl/") {
			importPath := strings.Trim(importSpec.Path.Value, `"`)
			for _, prefix := range forbiddenTrackerImportPrefixes {
				if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
					add(importSpec.Path.Pos(), "tracker implementations cannot import CLI, server, or workflow presentation owners")
					break
				}
			}
		}
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		typeName := selectedTypeName(literal.Type)
		if typeName == "SiteProfile" && strings.HasPrefix(relative, "internal/trackers/impl/unit3d/sites/") {
			checkUnit3DCustomNameVersion(literal, add)
			checkUnit3DCallbackOwnership(path, literal, add)
		}
		if _, forbidden := publicationCommandNames[typeName]; forbidden && !strings.HasPrefix(relative, "internal/releaseworkflow/") {
			add(literal.Pos(), "workflow publication commands cannot be constructed by adapters: "+typeName)
		}
		switch {
		case strings.HasPrefix(relative, "internal/preparedrelease/") && typeName == "Request" && selectedPackageName(literal.Type) == "api":
			add(literal.Pos(), "canonical preparation cannot reconstruct broad api.Request values")
		case (typeName == "PreparedReleaseDisplay" || typeName == "ProviderDisplay") &&
			!strings.HasPrefix(relative, "internal/preparedrelease/") && len(literal.Elts) > 0:
			add(literal.Pos(), "prepared-release display construction belongs to internal/preparedrelease")
		}
		return true
	})
	if hasPrincipalTrackerPayload && !hasReviewedUploadName {
		add(parsed.Package, "tracker principal payload fields must consume PreparationInput.ReviewedUploadName")
	}

	for _, declaration := range parsed.Decls {
		switch typed := declaration.(type) {
		case *ast.FuncDecl:
			if trackerLocalImplementationFile(relative) && strings.EqualFold(typed.Name.Name, "bannedGroups") &&
				filepath.Base(relative) != "banned_groups.go" {
				add(typed.Name.Pos(), "tracker static banned-group declarations belong in banned_groups.go")
			}
			if isTrackerUploadFile(relative) {
				if _, forbidden := trackerUploadNamingFunctions[typed.Name.Name]; forbidden {
					add(typed.Name.Pos(), "tracker naming algorithms belong in name.go")
				}
				if responsibility := trackerUploadResponsibility(typed.Name.Name); responsibility != "" {
					add(
						typed.Name.Pos(),
						fmt.Sprintf("tracker %s algorithms belong in %s.go, not upload.go", responsibility, responsibility),
					)
				}
			}
			if strings.HasPrefix(relative, "internal/trackers/impl/") {
				if _, naming := trackerUploadNamingFunctions[typed.Name.Name]; naming && filepath.Base(relative) != "name.go" {
					add(typed.Name.Pos(), "tracker naming algorithms belong in name.go")
				}
			}
			if outsideRuntimeOwner(relative) {
				if _, forbidden := hostActivationFunctions[typed.Name.Name]; forbidden {
					add(typed.Name.Pos(), "runtime activation sequencing belongs to webserver.RuntimeActivator")
				}
			}
		case *ast.GenDecl:
			for _, specification := range typed.Specs {
				if valueSpec, ok := specification.(*ast.ValueSpec); ok && trackerLocalImplementationFile(relative) &&
					filepath.Base(relative) != "banned_groups.go" {
					for _, name := range valueSpec.Names {
						normalized := strings.ToLower(name.Name)
						if strings.Contains(normalized, "banned") && strings.Contains(normalized, "group") {
							add(name.Pos(), "tracker static banned-group declarations belong in banned_groups.go")
						}
					}
				}
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if interfaceType, ok := typeSpec.Type.(*ast.InterfaceType); ok && workflowInterface(typeSpec.Name.Name) {
					checkWorkflowInterface(interfaceType, add)
				}
				if relative == "pkg/api/prepared_release.go" {
					checkCanonicalMaps(typeSpec.Type, add)
					if typeSpec.Name.Name == "NamingFacts" {
						checkCanonicalNamingFacts(typeSpec.Type, add)
					}
				}
				if strings.HasPrefix(relative, "pkg/api/") {
					checkSingleSourceContract(typeSpec.Name.Name, typeSpec.Type, add)
					checkWorkflowMediaPathContract(typeSpec.Name.Name, typeSpec.Type, add)
				}
				if strings.HasPrefix(relative, "internal/webserver/") && apiV1RouteRequestTypePattern.MatchString(typeSpec.Name.Name) {
					add(typeSpec.Name.Pos(), "browser and public workflow routes must use shared pkg/api request DTOs")
				}
				if strings.HasPrefix(relative, "internal/preparedrelease/") && (typeSpec.Name.Name == "Seed" || typeSpec.Name.Name == "envelope") {
					checkPreparedState(typeSpec.Type, add)
				}
				if relative == "internal/preparedrelease/state/state.go" && typeSpec.Name.Name == "State" {
					checkPreparedState(typeSpec.Type, add)
				}
			}
		}
	}
	return violations, nil
}

func isTrackerUploadFile(relative string) bool {
	return strings.HasPrefix(relative, "internal/trackers/impl/") && strings.HasSuffix(relative, "/upload.go")
}

func trackerLocalImplementationFile(relative string) bool {
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) >= 6 && slices.Equal(parts[:4], []string{"internal", "trackers", "impl", "standalone"}) {
		return true
	}
	return len(parts) >= 7 && slices.Equal(parts[:5], []string{"internal", "trackers", "impl", "unit3d", "sites"})
}

func trackerUploadResponsibility(name string) string {
	for _, rule := range trackerUploadResponsibilityPatterns {
		if rule.pattern.MatchString(name) {
			return rule.name
		}
	}
	return ""
}

func checkUnit3DCustomNameVersion(literal *ast.CompositeLit, add func(token.Pos, string)) {
	hasBuildName := false
	hasVersion := false
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		name, ok := field.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch name.Name {
		case "BuildName":
			identifier, isIdentifier := field.Value.(*ast.Ident)
			hasBuildName = !isIdentifier || identifier.Name != "nil"
		case "BuildNameVersion":
			value, isLiteral := field.Value.(*ast.BasicLit)
			hasVersion = !isLiteral || value.Kind != token.STRING || strings.Trim(value.Value, `"`) != ""
		}
	}
	if hasBuildName && !hasVersion {
		add(literal.Pos(), "custom Unit3D naming callbacks require BuildNameVersion")
	}
}

func checkUnit3DCallbackOwnership(profilePath string, literal *ast.CompositeLit, add func(token.Pos, string)) {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		name, ok := field.Key.(*ast.Ident)
		if !ok {
			continue
		}
		expectedFile, tracked := unit3DCallbackFiles[name.Name]
		if !tracked {
			continue
		}
		callback, ok := field.Value.(*ast.Ident)
		if ok && callback.Name == "nil" {
			continue
		}
		targetPath := filepath.Join(filepath.Dir(profilePath), expectedFile)
		if !ok || !goFileDeclaresFunction(targetPath, callback.Name) {
			add(
				field.Pos(),
				fmt.Sprintf("Unit3D %s callback must be declared in site-local %s", name.Name, expectedFile),
			)
		}
	}
}

func goFileDeclaresFunction(path string, name string) bool {
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, path, nil, 0)
	if err != nil {
		return false
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name {
			return true
		}
	}
	return false
}

func checkWorkflowMediaPathContract(typeName string, value ast.Expr, add func(token.Pos, string)) {
	if !strings.Contains(typeName, "ReleaseWorkflow") ||
		(!strings.Contains(typeName, "Media") && !strings.Contains(typeName, "Screenshot") &&
			!strings.Contains(typeName, "DVDMenu") && !strings.Contains(typeName, "UploadedImage")) {
		return
	}
	structure, ok := value.(*ast.StructType)
	if !ok {
		return
	}
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if name.Name == "SourcePath" {
				add(name.Pos(), "release media contracts must use workflow identity and opaque artifact IDs, not source paths")
			}
		}
	}
}

// selectedPackageName returns the qualifier from a direct package selector expression.
func selectedPackageName(value ast.Expr) string {
	selector, ok := value.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	identifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return identifier.Name
}

func selectedTypeName(value ast.Expr) string {
	switch typed := value.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typed.Sel.Name
	default:
		return ""
	}
}

// checkSingleSourceContract rejects fields that would let canonical operation
// contracts represent multiple sources or carry presentation correlation.
func checkSingleSourceContract(typeName string, value ast.Expr, add func(token.Pos, string)) {
	var forbidden map[string]struct{}
	switch typeName {
	case "Request":
		forbidden = map[string]struct{}{
			"Paths":                    {},
			"Mode":                     {},
			"ExternalIDSelections":     {},
			"PlaylistSelections":       {},
			"PlaylistSelectionsUseAll": {},
		}
	case "PrepareInput":
		forbidden = map[string]struct{}{"AdditionalPaths": {}, "CorrelationID": {}}
	default:
		return
	}
	structure, ok := value.(*ast.StructType)
	if !ok {
		return
	}
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if _, found := forbidden[name.Name]; found {
				if name.Name == "CorrelationID" {
					add(name.Pos(), "canonical preparation input cannot contain operation presentation correlation")
					continue
				}
				add(name.Pos(), "canonical release operation contracts are single-source: "+typeName+"."+name.Name)
			}
		}
	}
}

// checkFrontendFile enforces release-session ownership and rejects invalid
// progress, Job, mutation, and BDMV path-derivation patterns.
func checkFrontendFile(path string, relative string) ([]Violation, error) {
	if !strings.HasPrefix(relative, "webui/src/") || strings.HasSuffix(relative, ".d.ts") {
		return nil, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", relative, err)
	}
	text := string(content)
	testFile := strings.HasSuffix(relative, ".test.ts") || strings.HasSuffix(relative, ".test.tsx")
	var violations []Violation
	add := func(offset int, message string) {
		line, column := textPosition(text, offset)
		violations = append(violations, Violation{
			File:    relative,
			Line:    line,
			Column:  column,
			Message: message,
		})
	}
	if isReleasePage(relative) {
		for offset, line := range sourceLines(text) {
			if strings.Contains(line, " from ") && strings.Contains(line, "/api/") && !strings.Contains(line, "/api/generated/") {
				add(offset, "release pages must use a release-session facet, not production API clients")
			}
		}
		if !strings.HasSuffix(relative, ".test.ts") && !strings.HasSuffix(relative, ".test.tsx") {
			if offset := strings.Index(text, `role="progressbar"`); offset >= 0 {
				add(offset, "release pages cannot own operation progress; use WorkflowOperationProgress")
			}
		}
	}
	if !testFile {
		if strings.HasPrefix(relative, "webui/src/jobRegistry/") {
			add(0, "legacy release Job package is removed")
		}
		for _, marker := range []string{"jobsClient", "jobs:update", "useReleaseJobs", "workflowOnly"} {
			if offset := strings.Index(text, marker); offset >= 0 {
				add(offset, "legacy release fallback or Job coordination is removed: "+marker)
			}
		}
		if match := optionalWorkflowPortPattern.FindStringIndex(text); match != nil {
			add(match[0], "release workflow transport must be mandatory")
		}
		for _, marker := range callerVisibleUploadPlanMarkers {
			if offset := strings.Index(text, marker); offset >= 0 {
				add(offset, "callers cannot build or execute a release upload plan: "+marker)
			}
		}
	}
	if relative == "webui/src/api/generated/release-workflow.ts" &&
		!strings.Contains(text, "// Code generated by cmd/workflowcontractgen. DO NOT EDIT.") {
		add(0, "workflow transport types must be generated by cmd/workflowcontractgen")
	}
	if relative == "webui/src/releaseSession/types.ts" {
		for _, marker := range []string{"Dispatch<", "SetStateAction", "MutableRefObject", "RefObject", "dispatch:"} {
			if offset := strings.Index(text, marker); offset >= 0 {
				add(offset, "release-session facets cannot expose React mutation primitives: "+marker)
			}
		}
		if match := preparationIntentTrackersPattern.FindStringIndex(text); match != nil {
			add(match[0], "frontend PreparationIntent cannot contain workflow tracker selection")
		}
	}
	if !strings.HasPrefix(relative, "webui/src/releaseSession/") {
		if offset := strings.Index(text, "preparation:progress"); offset >= 0 {
			add(offset, "preparation progress subscription belongs to webui/src/releaseSession")
		}
	}
	if strings.HasPrefix(relative, "webui/src/releaseSession/") ||
		strings.HasPrefix(relative, "webui/src/api/") {
		for offset, line := range sourceLines(text) {
			if match := messageRecoveryPattern.FindStringIndex(line); match != nil {
				add(offset+match[0], "operation recovery cannot be inferred from error-message substrings")
			}
		}
	}
	for offset, line := range sourceLines(text) {
		if match := frontendBDMVPathPattern.FindStringIndex(line); match != nil {
			add(offset+match[0], "frontend cannot derive BDMV resource paths from preparation sources")
		}
	}
	return violations, nil
}

func isReleasePage(relative string) bool {
	const prefix = "webui/src/pages/"
	if !strings.HasPrefix(relative, prefix) {
		return false
	}
	remainder := strings.TrimPrefix(relative, prefix)
	directory, _, _ := strings.Cut(remainder, "/")
	_, found := releasePageDirectories[directory]
	return found
}

func sourceLines(value string) map[int]string {
	lines := make(map[int]string)
	offset := 0
	for line := range strings.SplitSeq(value, "\n") {
		lines[offset] = line
		offset += len(line) + 1
	}
	return lines
}

func textPosition(value string, offset int) (int, int) {
	line, column := 1, 1
	for index := 0; index < offset && index < len(value); index++ {
		if value[index] == '\n' {
			line++
			column = 1
		} else {
			column++
		}
	}
	return line, column
}

func checkCanonicalNamingFacts(value ast.Expr, add func(token.Pos, string)) {
	structure, ok := value.(*ast.StructType)
	if !ok {
		return
	}
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if name.Name == "Category" {
				add(name.Pos(), "canonical top-level category belongs only to ExternalIdentity")
			}
		}
	}
}

func workflowInterface(name string) bool {
	for _, marker := range workflowInterfaceMarkers {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func checkWorkflowInterface(value *ast.InterfaceType, add func(token.Pos, string)) {
	for _, field := range value.Methods.List {
		function, ok := field.Type.(*ast.FuncType)
		if !ok || function.Params == nil {
			continue
		}
		for _, parameter := range function.Params.List {
			if typeNamesPreparedRelease(parameter.Type) {
				add(parameter.Pos(), "workflow interfaces must accept owner-local inputs, not PreparedRelease")
			}
		}
	}
}

func typeNamesPreparedRelease(value ast.Expr) bool {
	switch typed := value.(type) {
	case *ast.Ident:
		return typed.Name == "PreparedRelease"
	case *ast.SelectorExpr:
		return typed.Sel.Name == "PreparedRelease"
	case *ast.ArrayType:
		return typeNamesPreparedRelease(typed.Elt)
	case *ast.StarExpr:
		return typeNamesPreparedRelease(typed.X)
	default:
		return false
	}
}

func checkCanonicalMaps(value ast.Expr, add func(token.Pos, string)) {
	ast.Inspect(value, func(node ast.Node) bool {
		mapping, ok := node.(*ast.MapType)
		if !ok {
			return true
		}
		key, keyOK := mapping.Key.(*ast.Ident)
		if keyOK && key.Name == "string" && isAny(mapping.Value) {
			add(mapping.Pos(), "canonical prepared facts cannot use map[string]any")
		}
		return true
	})
}

func isAny(value ast.Expr) bool {
	if identifier, ok := value.(*ast.Ident); ok {
		return identifier.Name == "any"
	}
	interfaceType, ok := value.(*ast.InterfaceType)
	return ok && (interfaceType.Methods == nil || len(interfaceType.Methods.List) == 0)
}

func checkPreparedState(value ast.Expr, add func(token.Pos, string)) {
	structure, ok := value.(*ast.StructType)
	if !ok {
		return
	}
	for _, field := range structure.Fields.List {
		for _, name := range field.Names {
			if _, forbidden := preparedStateFields[name.Name]; forbidden {
				add(name.Pos(), "prepared release collection and transfer state cannot retain workflow state: "+name.Name)
			}
		}
	}
}

func outsideRuntimeOwner(relative string) bool {
	return !strings.HasPrefix(relative, "internal/webserver/")
}
