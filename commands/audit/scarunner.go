package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jfrog/build-info-go/utils/pythonutils"
	"github.com/jfrog/gofrog/datastructures"
	"github.com/jfrog/jfrog-cli-core/v2/common/project"
	"github.com/jfrog/jfrog-cli-core/v2/utils/config"
	"github.com/jfrog/jfrog-cli-core/v2/utils/coreutils"
	"github.com/jfrog/jfrog-cli-core/v2/utils/java"
	"github.com/jfrog/jfrog-cli-security/commands/audit/sca"
	_go "github.com/jfrog/jfrog-cli-security/commands/audit/sca/go"
	"github.com/jfrog/jfrog-cli-security/commands/audit/sca/npm"
	"github.com/jfrog/jfrog-cli-security/commands/audit/sca/nuget"
	"github.com/jfrog/jfrog-cli-security/commands/audit/sca/python"
	"github.com/jfrog/jfrog-cli-security/commands/audit/sca/yarn"
	xrayConfig "github.com/jfrog/jfrog-cli-security/config"
	"github.com/jfrog/jfrog-cli-security/scangraph"
	xrayutils "github.com/jfrog/jfrog-cli-security/utils"
	"github.com/jfrog/jfrog-client-go/artifactory/services/fspatterns"
	clientutils "github.com/jfrog/jfrog-client-go/utils"
	"github.com/jfrog/jfrog-client-go/utils/errorutils"
	"github.com/jfrog/jfrog-client-go/utils/log"
	"github.com/jfrog/jfrog-client-go/xray/services"
	xrayCmdUtils "github.com/jfrog/jfrog-client-go/xray/services/utils"
)

var DefaultExcludePatterns = []string{"*.git*", "*node_modules*", "*target*", "*venv*", "*test*"}

func runScaScan(params *AuditParams, results *xrayutils.Results) (err error) {
	// Prepare
	currentWorkingDir, err := os.Getwd()
	if errorutils.CheckError(err) != nil {
		return
	}
	serverDetails, err := params.ServerDetails()
	if err != nil {
		return
	}

	scans := getScaScansToPreform(params)
	if len(scans) == 0 {
		log.Info("Couldn't determine a package manager or build tool used by this project. Skipping the SCA scan...")
		return
	}
	scanInfo, err := coreutils.GetJsonIndent(scans)
	if err != nil {
		return
	}
	log.Info(fmt.Sprintf("Preforming %d SCA scans:\n%s", len(scans), scanInfo))

	defer func() {
		// Make sure to return to the original working directory, executeScaScan may change it
		err = errors.Join(err, os.Chdir(currentWorkingDir))
	}()
	for _, scan := range scans {
		// Run the scan
		log.Info("Running SCA scan for", scan.Technology, "vulnerable dependencies in", scan.WorkingDirectory, "directory...")
		if wdScanErr := executeScaScan(serverDetails, params, scan); wdScanErr != nil {
			err = errors.Join(err, fmt.Errorf("audit command in '%s' failed:\n%s", scan.WorkingDirectory, wdScanErr.Error()))
			continue
		}
		// Add the scan to the results
		results.ScaResults = append(results.ScaResults, *scan)
	}
	return
}

// Calculate the scans to preform
func getScaScansToPreform(params *AuditParams) (scansToPreform []*xrayutils.ScaScanResult) {
	for _, requestedDirectory := range params.workingDirs {
		// Detect descriptors and technologies in the requested directory.
		techToWorkingDirs, err := coreutils.DetectTechnologiesDescriptors(requestedDirectory, params.isRecursiveScan, params.Technologies(), getRequestedDescriptors(params), getExcludePattern(params, params.isRecursiveScan))
		if err != nil {
			log.Warn("Couldn't detect technologies in", requestedDirectory, "directory.", err.Error())
			continue
		}
		// Create scans to preform
		for tech, workingDirs := range techToWorkingDirs {
			if tech == coreutils.Dotnet {
				// We detect Dotnet and Nuget the same way, if one detected so does the other.
				// We don't need to scan for both and get duplicate results.
				continue
			}
			if len(workingDirs) == 0 {
				// Requested technology (from params) descriptors/indicators was not found, scan only requested directory for this technology.
				scansToPreform = append(scansToPreform, &xrayutils.ScaScanResult{WorkingDirectory: requestedDirectory, Technology: tech})
			}
			for workingDir, descriptors := range workingDirs {
				// Add scan for each detected working directory.
				scansToPreform = append(scansToPreform, &xrayutils.ScaScanResult{WorkingDirectory: workingDir, Technology: tech, Descriptors: descriptors})
			}
		}
	}
	return
}

func getRequestedDescriptors(params *AuditParams) map[coreutils.Technology][]string {
	requestedDescriptors := map[coreutils.Technology][]string{}
	if params.PipRequirementsFile() != "" {
		requestedDescriptors[coreutils.Pip] = []string{params.PipRequirementsFile()}
	}
	return requestedDescriptors
}

func getExcludePattern(params *AuditParams, recursive bool) string {
	exclusions := params.Exclusions()
	if len(exclusions) == 0 {
		exclusions = append(exclusions, DefaultExcludePatterns...)
	}
	return fspatterns.PrepareExcludePathPattern(exclusions, clientutils.WildCardPattern, recursive)
}

// Preform the SCA scan for the given scan information.
// This method will change the working directory to the scan's working directory.
func executeScaScan(serverDetails *config.ServerDetails, params *AuditParams, scan *xrayutils.ScaScanResult) (err error) {
	// Get the dependency tree for the technology in the working directory.
	if err = os.Chdir(scan.WorkingDirectory); err != nil {
		return errorutils.CheckError(err)
	}
	flattenTree, fullDependencyTrees, techErr := GetTechDependencyTree(params.AuditBasicParams, scan.Technology)
	if techErr != nil {
		return fmt.Errorf("failed while building '%s' dependency tree:\n%s", scan.Technology, techErr.Error())
	}
	if flattenTree == nil || len(flattenTree.Nodes) == 0 {
		return errorutils.CheckErrorf("no dependencies were found. Please try to build your project and re-run the audit command")
	}
	// Scan the dependency tree.
	scanResults, xrayErr := runScaWithTech(scan.Technology, params, serverDetails, flattenTree, fullDependencyTrees)
	if xrayErr != nil {
		return fmt.Errorf("'%s' Xray dependency tree scan request failed:\n%s", scan.Technology, xrayErr.Error())
	}
	scan.IsMultipleRootProject = clientutils.Pointer(len(fullDependencyTrees) > 1)
	addThirdPartyDependenciesToParams(params, scan.Technology, flattenTree, fullDependencyTrees)
	scan.XrayResults = append(scan.XrayResults, scanResults...)
	return
}

func runScaWithTech(tech coreutils.Technology, params *AuditParams, serverDetails *config.ServerDetails, flatTree *xrayCmdUtils.GraphNode, fullDependencyTrees []*xrayCmdUtils.GraphNode) (techResults []services.ScanResponse, err error) {
	scanGraphParams := scangraph.NewScanGraphParams().
		SetServerDetails(serverDetails).
		SetXrayGraphScanParams(params.xrayGraphScanParams).
		SetXrayVersion(params.xrayVersion).
		SetFixableOnly(params.fixableOnly).
		SetSeverityLevel(params.minSeverityFilter)
	techResults, err = sca.RunXrayDependenciesTreeScanGraph(flatTree, params.Progress(), tech, scanGraphParams)
	if err != nil {
		return
	}
	techResults = sca.BuildImpactPathsForScanResponse(techResults, fullDependencyTrees)
	return
}

func addThirdPartyDependenciesToParams(params *AuditParams, tech coreutils.Technology, flatTree *xrayCmdUtils.GraphNode, fullDependencyTrees []*xrayCmdUtils.GraphNode) {
	var dependenciesForApplicabilityScan []string
	if shouldUseAllDependencies(params.thirdPartyApplicabilityScan, tech) {
		dependenciesForApplicabilityScan = getDirectDependenciesFromTree([]*xrayCmdUtils.GraphNode{flatTree})
	} else {
		dependenciesForApplicabilityScan = getDirectDependenciesFromTree(fullDependencyTrees)
	}
	params.AppendDependenciesForApplicabilityScan(dependenciesForApplicabilityScan)
}

// When building pip dependency tree using pipdeptree, some of the direct dependencies are recognized as transitive and missed by the CA scanner.
// Our solution for this case is to send all dependencies to the CA scanner.
// When thirdPartyApplicabilityScan is true, use flatten graph to include all the dependencies in applicability scanning.
// Only npm is supported for this flag.
func shouldUseAllDependencies(thirdPartyApplicabilityScan bool, tech coreutils.Technology) bool {
	return tech == coreutils.Pip || (thirdPartyApplicabilityScan && tech == coreutils.Npm)
}

// This function retrieves the dependency trees of the scanned project and extracts a set that contains only the direct dependencies.
func getDirectDependenciesFromTree(dependencyTrees []*xrayCmdUtils.GraphNode) []string {
	directDependencies := datastructures.MakeSet[string]()
	for _, tree := range dependencyTrees {
		for _, node := range tree.Nodes {
			directDependencies.Add(node.Id)
		}
	}
	return directDependencies.ToSlice()
}

func GetTechDependencyTree(params xrayutils.AuditParams, tech coreutils.Technology) (flatTree *xrayCmdUtils.GraphNode, fullDependencyTrees []*xrayCmdUtils.GraphNode, err error) {
	logMessage := fmt.Sprintf("Calculating %s dependencies", tech.ToFormal())
	log.Info(logMessage + "...")
	if params.Progress() != nil {
		params.Progress().SetHeadlineMsg(logMessage)
	}
	serverDetails, err := params.ServerDetails()
	if err != nil {
		return
	}
	err = SetResolutionRepoIfExists(params, tech)
	if err != nil {
		return
	}
	var uniqueDeps []string
	var uniqDepsWithTypes map[string][]string
	startTime := time.Now()
	switch tech {
	case coreutils.Maven, coreutils.Gradle:
		curationCacheFolder := ""
		if params.IsCurationCmd() {
			curationCacheFolder, err = xrayConfig.GetCurationMavenCacheFolder()
			if err != nil {
				return
			}
		}
		fullDependencyTrees, uniqDepsWithTypes, err = java.BuildDependencyTree(java.DepTreeParams{
			Server:                  serverDetails,
			DepsRepo:                params.DepsRepo(),
			IsMavenDepTreeInstalled: params.IsMavenDepTreeInstalled(),
			IsCurationCmd:           params.IsCurationCmd(),
			CurationCacheFolder:     curationCacheFolder,
		}, tech)
	case coreutils.Npm:
		fullDependencyTrees, uniqueDeps, err = npm.BuildDependencyTree(params)
	case coreutils.Yarn:
		fullDependencyTrees, uniqueDeps, err = yarn.BuildDependencyTree(params)
	case coreutils.Go:
		fullDependencyTrees, uniqueDeps, err = _go.BuildDependencyTree(params)
	case coreutils.Pipenv, coreutils.Pip, coreutils.Poetry:
		fullDependencyTrees, uniqueDeps, err = python.BuildDependencyTree(&python.AuditPython{
			Server:              serverDetails,
			Tool:                pythonutils.PythonTool(tech),
			RemotePypiRepo:      params.DepsRepo(),
			PipRequirementsFile: params.PipRequirementsFile()})
	case coreutils.Nuget:
		fullDependencyTrees, uniqueDeps, err = nuget.BuildDependencyTree(params)
	default:
		err = errorutils.CheckErrorf("%s is currently not supported", string(tech))
	}
	if err != nil || (len(uniqueDeps) == 0 && uniqDepsWithTypes == nil) {
		return
	}
	log.Debug(fmt.Sprintf("Created '%s' dependency tree with %d nodes. Elapsed time: %.1f seconds.", tech.ToFormal(), len(uniqueDeps), time.Since(startTime).Seconds()))
	if uniqDepsWithTypes != nil {
		flatTree, err = createFlatTreeWithTypes(uniqDepsWithTypes)
		return
	}
	flatTree, err = createFlatTree(uniqueDeps)
	return
}

// Associates a technology with another of a different type in the structure.
// Docker is not present, as there is no docker-config command and, consequently, no docker.yaml file we need to operate on.
var TechType = map[coreutils.Technology]project.ProjectType{
	coreutils.Maven: project.Maven, coreutils.Gradle: project.Gradle, coreutils.Npm: project.Npm, coreutils.Yarn: project.Yarn, coreutils.Go: project.Go, coreutils.Pip: project.Pip,
	coreutils.Pipenv: project.Pipenv, coreutils.Poetry: project.Poetry, coreutils.Nuget: project.Nuget, coreutils.Dotnet: project.Dotnet,
}

// Verifies the existence of depsRepo. If it doesn't exist, it searches for a configuration file based on the technology type. If found, it assigns depsRepo in the AuditParams.
func SetResolutionRepoIfExists(params xrayutils.AuditParams, tech coreutils.Technology) (err error) {
	if params.DepsRepo() != "" || params.IgnoreConfigFile() {
		return
	}

	configFilePath, exists, err := project.GetProjectConfFilePath(TechType[tech])
	if err != nil {
		err = fmt.Errorf("failed while searching for %s.yaml config file: %s", tech.String(), err.Error())
		return
	}
	if !exists {
		// Nuget and Dotnet are identified similarly in the detection process. To prevent redundancy, Dotnet is filtered out earlier in the process, focusing solely on detecting Nuget.
		// Consequently, it becomes necessary to verify the presence of dotnet.yaml when Nuget detection occurs.
		if tech == coreutils.Nuget {
			configFilePath, exists, err = project.GetProjectConfFilePath(TechType[coreutils.Dotnet])
			if err != nil {
				err = fmt.Errorf("failed while searching for %s.yaml config file: %s", tech.String(), err.Error())
				return
			}
			if !exists {
				log.Debug(fmt.Sprintf("No %s.yaml nor %s.yaml configuration file was found. Resolving dependencies from %s default registry", coreutils.Nuget.String(), coreutils.Dotnet.String(), tech.String()))
				return
			}
		} else {
			log.Debug(fmt.Sprintf("No %s.yaml configuration file was found. Resolving dependencies from %s default registry", tech.String(), tech.String()))
			return
		}
	}

	log.Debug("Using resolver config from", configFilePath)
	repoConfig, err := project.ReadResolutionOnlyConfiguration(configFilePath)
	if err != nil {
		err = fmt.Errorf("failed while reading %s.yaml config file: %s", tech.String(), err.Error())
		return
	}
	details, err := repoConfig.ServerDetails()
	if err != nil {
		err = fmt.Errorf("failed getting server details: %s", err.Error())
		return
	}
	params.SetServerDetails(details)
	params.SetDepsRepo(repoConfig.TargetRepo())
	return
}

func createFlatTreeWithTypes(uniqueDeps map[string][]string) (*xrayCmdUtils.GraphNode, error) {
	if err := logDeps(uniqueDeps); err != nil {
		return nil, err
	}
	var uniqueNodes []*xrayCmdUtils.GraphNode
	for uniqueDep, types := range uniqueDeps {
		p := types
		uniqueNodes = append(uniqueNodes, &xrayCmdUtils.GraphNode{Id: uniqueDep, Types: &p})
	}
	return &xrayCmdUtils.GraphNode{Id: "root", Nodes: uniqueNodes}, nil
}

func createFlatTree(uniqueDeps []string) (*xrayCmdUtils.GraphNode, error) {
	if err := logDeps(uniqueDeps); err != nil {
		return nil, err
	}
	uniqueNodes := []*xrayCmdUtils.GraphNode{}
	for _, uniqueDep := range uniqueDeps {
		uniqueNodes = append(uniqueNodes, &xrayCmdUtils.GraphNode{Id: uniqueDep})
	}
	return &xrayCmdUtils.GraphNode{Id: "root", Nodes: uniqueNodes}, nil
}

func logDeps(uniqueDeps any) error {
	if log.GetLogger().GetLogLevel() == log.DEBUG {
		// Avoid printing and marshaling if not on DEBUG mode.
		jsonList, err := json.Marshal(uniqueDeps)
		if errorutils.CheckError(err) != nil {
			return err
		}
		log.Debug("Unique dependencies list:\n" + clientutils.IndentJsonArray(jsonList))
	}
	return nil
}
