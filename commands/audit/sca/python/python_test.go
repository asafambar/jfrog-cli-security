package python

import (
	"github.com/jfrog/jfrog-cli-core/v2/utils/coreutils"
	xrayutils "github.com/jfrog/jfrog-cli-security/utils"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jfrog/jfrog-cli-core/v2/utils/tests"
	"github.com/jfrog/jfrog-cli-security/commands/audit/sca"
	"github.com/stretchr/testify/assert"
)

func TestBuildPipDependencyListSetuppy(t *testing.T) {
	// Create and change directory to test workspace
	_, cleanUp := sca.CreateTestWorkspace(t, filepath.Join("projects", "package-managers", "python", "pip", "pip", "setuppyproject"))
	defer cleanUp()
	// Run getModulesDependencyTrees
	rootNode, uniqueDeps, err := BuildDependencyTree(nil, coreutils.Pip, &xrayutils.AuditBasicParams{})
	assert.NoError(t, err)
	assert.Contains(t, uniqueDeps, PythonPackageTypeIdentifier+"pexpect:4.8.0")
	assert.Contains(t, uniqueDeps, PythonPackageTypeIdentifier+"ptyprocess:0.7.0")
	assert.Contains(t, uniqueDeps, PythonPackageTypeIdentifier+"pip-example:1.2.3")
	assert.Len(t, rootNode, 1)
	if len(rootNode) > 0 {
		assert.NotEmpty(t, rootNode[0].Nodes)
		if rootNode[0].Nodes != nil {
			// Test direct dependency
			directDepNode := tests.GetAndAssertNode(t, rootNode[0].Nodes, "pip-example:1.2.3")
			// Test child module
			childNode := tests.GetAndAssertNode(t, directDepNode.Nodes, "pexpect:4.8.0")
			// Test sub child module
			tests.GetAndAssertNode(t, childNode.Nodes, "ptyprocess:0.7.0")
		}
	}
}

func TestBuildPipDependencyListSetuppyForCuration(t *testing.T) {
	// Create and change directory to test workspace
	_, cleanUp := sca.CreateTestWorkspace(t, filepath.Join("projects", "package-managers", "python", "pip", "pip", "setuppyproject"))
	defer cleanUp()
	// Run getModulesDependencyTrees
	params := &xrayutils.AuditBasicParams{}
	params.SetIsCurationCmd(true)
	rootNode, uniqueDeps, err := BuildDependencyTree(nil, coreutils.Pip, params)
	assert.NoError(t, err)
	assert.Contains(t, uniqueDeps, PythonPackageTypeIdentifier+"pexpect:4.8.0")
	assert.Contains(t, uniqueDeps, PythonPackageTypeIdentifier+"ptyprocess:0.7.0")
	assert.Contains(t, uniqueDeps, PythonPackageTypeIdentifier+"pip-example:1.2.3")
	assert.Len(t, rootNode, 1)
	if len(rootNode) > 0 {
		assert.NotEmpty(t, rootNode[0].Nodes)
		if rootNode[0].Nodes != nil {
			// Test direct dependency
			directDepNode := tests.GetAndAssertNode(t, rootNode[0].Nodes, "pip-example:1.2.3")
			// Test child module
			childNode := tests.GetAndAssertNode(t, directDepNode.Nodes, "pexpect:4.8.0")
			// Test sub child module
			tests.GetAndAssertNode(t, childNode.Nodes, "ptyprocess:0.7.0")

			downloadUrls := params.GetDownloadUrls()
			assert.NotEmpty(t, downloadUrls)
			url, exist := downloadUrls[PythonPackageTypeIdentifier+"ptyprocess:0.7.0"]
			assert.True(t, exist)
			strings.HasSuffix(url, "packages/packages/22/a6/858897256d0deac81")

			url, exist = downloadUrls[PythonPackageTypeIdentifier+"pexpect:4.8.0"]
			assert.True(t, exist)
			strings.HasSuffix(url, "packages/packages/39/7b/88dbb785881c28a10")

		}
	}
}

func TestPipDependencyListRequirementsFallback(t *testing.T) {
	// Create and change directory to test workspace
	_, cleanUp := sca.CreateTestWorkspace(t, filepath.Join("projects", "package-managers", "python", "pip", "pip", "requirementsproject"))
	defer cleanUp()
	// No requirements file field specified, expect the command to use the fallback 'pip install -r requirements.txt' command
	rootNode, uniqueDeps, err := BuildDependencyTree(nil, coreutils.Pip, &xrayutils.AuditBasicParams{})
	assert.NoError(t, err)
	assert.Contains(t, uniqueDeps, PythonPackageTypeIdentifier+"pexpect:4.7.0")
	assert.Contains(t, uniqueDeps, PythonPackageTypeIdentifier+"ptyprocess:0.7.0")
	assert.Len(t, rootNode, 1)
	if assert.GreaterOrEqual(t, len(rootNode[0].Nodes), 2) {
		childNode := tests.GetAndAssertNode(t, rootNode[0].Nodes, "pexpect:4.7.0")
		if childNode != nil {
			// Test child module
			tests.GetAndAssertNode(t, childNode.Nodes, "ptyprocess:0.7.0")
		}
	}
}

func TestBuildPipDependencyListRequirements(t *testing.T) {
	// Create and change directory to test workspace
	_, cleanUp := sca.CreateTestWorkspace(t, filepath.Join("projects", "package-managers", "python", "pip", "pip", "requirementsproject"))
	defer cleanUp()
	// Run getModulesDependencyTrees
	params := &xrayutils.AuditBasicParams{}
	params.SetPipRequirementsFile("requirements.txt")
	rootNode, uniqueDeps, err := BuildDependencyTree(nil, coreutils.Pypi, params)
	assert.NoError(t, err)
	assert.Contains(t, uniqueDeps, PythonPackageTypeIdentifier+"pexpect:4.7.0")
	assert.Contains(t, uniqueDeps, PythonPackageTypeIdentifier+"ptyprocess:0.7.0")
	assert.Len(t, rootNode, 1)
	if len(rootNode) > 0 {
		assert.NotEmpty(t, rootNode[0].Nodes)
		if rootNode[0].Nodes != nil {
			// Test root module
			directDepNode := tests.GetAndAssertNode(t, rootNode[0].Nodes, "pexpect:4.7.0")
			// Test child module
			tests.GetAndAssertNode(t, directDepNode.Nodes, "ptyprocess:0.7.0")
		}
	}
}

func TestBuildPipenvDependencyList(t *testing.T) {
	// Create and change directory to test workspace
	_, cleanUp := sca.CreateTestWorkspace(t, filepath.Join("projects", "package-managers", "python", "pipenv", "pipenv", "pipenvproject"))
	defer cleanUp()
	expectedPipenvUniqueDeps := []string{
		PythonPackageTypeIdentifier + "toml:0.10.2",
		PythonPackageTypeIdentifier + "pexpect:4.8.0",
		PythonPackageTypeIdentifier + "ptyprocess:0.7.0",
	}
	// Run getModulesDependencyTrees
	rootNode, uniqueDeps, err := BuildDependencyTree(nil, coreutils.Pypi, &xrayutils.AuditBasicParams{})
	if err != nil {
		t.Fatal(err)
	}
	assert.ElementsMatch(t, uniqueDeps, expectedPipenvUniqueDeps, "First is actual, Second is Expected")
	assert.Len(t, rootNode, 1)
	if len(rootNode) > 0 {
		assert.NotEmpty(t, rootNode[0].Nodes)
		// Test child module
		childNode := tests.GetAndAssertNode(t, rootNode[0].Nodes, "pexpect:4.8.0")
		// Test sub child module
		if assert.NotNil(t, childNode) {
			tests.GetAndAssertNode(t, childNode.Nodes, "ptyprocess:0.7.0")
		}
	}
}

func TestBuildPoetryDependencyList(t *testing.T) {
	// Create and change directory to test workspace
	_, cleanUp := sca.CreateTestWorkspace(t, filepath.Join("projects", "package-managers", "python", "poetry", "my-poetry-project"))
	defer cleanUp()
	expectedPoetryUniqueDeps := []string{
		PythonPackageTypeIdentifier + "wcwidth:0.2.8",
		PythonPackageTypeIdentifier + "colorama:0.4.6",
		PythonPackageTypeIdentifier + "packaging:23.2",
		PythonPackageTypeIdentifier + "python:",
		PythonPackageTypeIdentifier + "pluggy:0.13.1",
		PythonPackageTypeIdentifier + "py:1.11.0",
		PythonPackageTypeIdentifier + "atomicwrites:1.4.1",
		PythonPackageTypeIdentifier + "attrs:23.1.0",
		PythonPackageTypeIdentifier + "more-itertools:10.1.0",
		PythonPackageTypeIdentifier + "numpy:1.26.1",
		PythonPackageTypeIdentifier + "pytest:5.4.3",
	}
	// Run getModulesDependencyTrees
	rootNode, uniqueDeps, err := BuildDependencyTree(nil, coreutils.Pypi, &xrayutils.AuditBasicParams{})
	if err != nil {
		t.Fatal(err)
	}
	assert.ElementsMatch(t, uniqueDeps, expectedPoetryUniqueDeps, "First is actual, Second is Expected")
	assert.Len(t, rootNode, 1)
	if len(rootNode) > 0 {
		assert.NotEmpty(t, rootNode[0].Nodes)
		// Test child module
		childNode := tests.GetAndAssertNode(t, rootNode[0].Nodes, "pytest:5.4.3")
		// Test sub child module
		if assert.NotNil(t, childNode) {
			tests.GetAndAssertNode(t, childNode.Nodes, "packaging:23.2")
		}
	}
}

func TestGetPipInstallArgs(t *testing.T) {
	assert.Equal(t, []string{"-m", "pip", "install", "."}, getPipInstallArgs("", "", "", ""))
	assert.Equal(t, []string{"-m", "pip", "install", "-r", "requirements.txt"}, getPipInstallArgs("requirements.txt", "", "", ""))

	assert.Equal(t, []string{"-m", "pip", "install", ".", "-i", "https://user@pass:remote.url/repo"}, getPipInstallArgs("", "https://user@pass:remote.url/repo", "", ""))
	assert.Equal(t, []string{"-m", "pip", "install", "-r", "requirements.txt", "-i", "https://user@pass:remote.url/repo"}, getPipInstallArgs("requirements.txt", "https://user@pass:remote.url/repo", "", ""))
}
