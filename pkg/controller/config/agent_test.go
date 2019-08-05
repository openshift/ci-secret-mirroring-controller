package config

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	config1Str = `
secrets:
- from:
    namespace: source-namespace-1
    name: dev-secret-1
  to:
    namespace: target-namespace-2
    name: prod-secret-1
`
	config2Str = `
secrets:
- from:
    namespace: source-namespace-1
    name: dev-secret-1
  to:
    namespace: target-namespace-2
    name: prod-secret-1
- from:
    namespace: source-namespace-3
    name: dev-secret-1
  to:
    namespace: target-namespace-4
    name: prod-secret-1
`
	date1 = "..2019_08_02_18_16_30.006116574"
	date2 = "..2019_08_03_18_16_30.006116574"
)

var (
	unitUnderTest someTestClass
)

type someTestClass struct {
	config Getter
}

func TestConfigWithFile(t *testing.T) {

	content := []byte(config1Str)
	configFile, err := ioutil.TempFile("", "testConfig.*.txt")
	defer func() {
		err := configFile.Close()
		if err != nil {
			t.Errorf("expected no error (configFile.Close) but got one: %v", err)
		}
		err = os.Remove(configFile.Name())
		if err != nil {
			t.Errorf("expected no error (os.Remove) but got one: %v", err)
		}
	}()

	if err != nil {
		t.Errorf("expected no error but got one: %v", err)
	}

	if _, err := configFile.Write(content); err != nil {
		t.Errorf("expected no error (configFile.Write) but got one: %v", err)
	}

	configAgent := &Agent{}
	if err := configAgent.Start(configFile.Name()); err != nil {
		t.Errorf("expected no error (configAgent.Start) but got one: %v", err)
	}

	unitUnderTest = someTestClass{config: configAgent.Config}

	result := unitUnderTest.config()
	expected := &Configuration{
		Secrets: []MirrorConfig{
			{
				From: SecretLocation{Namespace: "source-namespace-1", Name: "dev-secret-1"},
				To:   SecretLocation{Namespace: "target-namespace-2", Name: "prod-secret-1"},
			},
		},
	}

	if !reflect.DeepEqual(expected, result) {
		t.Errorf("Unexpected mis-match: %s", diff.ObjectReflectDiff(expected, result))
	}

	content = []byte(config2Str)
	if _, err := configFile.Write(content); err != nil {
		t.Errorf("expected no error (configFile.Write) but got one: %v", err)
	}

	expected = &Configuration{
		Secrets: []MirrorConfig{
			{
				From: SecretLocation{Namespace: "source-namespace-1", Name: "dev-secret-1"},
				To:   SecretLocation{Namespace: "target-namespace-2", Name: "prod-secret-1"},
			},
			{
				From: SecretLocation{Namespace: "source-namespace-3", Name: "dev-secret-1"},
				To:   SecretLocation{Namespace: "target-namespace-4", Name: "prod-secret-1"},
			},
		},
	}

	err = wait.Poll(1*time.Second, 10*time.Second,
		func() (bool, error) {
			result = unitUnderTest.config()
			if !reflect.DeepEqual(expected, result) {
				return false, nil
			}
			return true, nil
		})
	if err != nil {
		t.Errorf("expected no error (wait.Poll) but got one: %v", err)
	}
}

func TestConfigWithSymlink(t *testing.T) {
	testDir, err := ioutil.TempDir("", "testConfig-folder-")
	if err != nil {
		t.Errorf("expected no error (ioutil.TempDir) but got one: %v", err)
	}
	dateDir := filepath.Join(testDir, date1)
	err = os.Mkdir(dateDir, 0755)
	if err != nil {
		t.Errorf("expected no error (os.Mkdir) but got one: %v", err)
	}

	mappingFileNameInDateDir := filepath.Join(dateDir, "mapping.yaml")
	if err = ioutil.WriteFile(mappingFileNameInDateDir, []byte(config1Str), 0644); err != nil {
		t.Errorf("expected no error (configFile.Write) but got one: %v", err)
	}

	if err := os.Symlink(date1, filepath.Join(testDir, "..data")); err != nil {
		t.Errorf("Failed to create sylink: %s", err)
	}

	mappingFileName := filepath.Join(testDir, "mapping.yaml")
	if err := os.Symlink("..data/mapping.yaml", mappingFileName); err != nil {
		t.Errorf("Failed to create sylink: %s", err)
	}

	configAgent := &Agent{}
	if err := configAgent.Start(mappingFileName); err != nil {
		t.Errorf("expected no error (configAgent.Start) but got one: %v", err)
	}

	unitUnderTest = someTestClass{config: configAgent.Config}

	result := unitUnderTest.config()
	expected := &Configuration{
		Secrets: []MirrorConfig{
			{
				From: SecretLocation{Namespace: "source-namespace-1", Name: "dev-secret-1"},
				To:   SecretLocation{Namespace: "target-namespace-2", Name: "prod-secret-1"},
			},
		},
	}

	if !reflect.DeepEqual(expected, result) {
		t.Errorf("Unexpected mis-match: %s", diff.ObjectReflectDiff(expected, result))
	}

	dateDir2 := filepath.Join(testDir, date2)
	err = os.Mkdir(dateDir2, 0755)
	if err != nil {
		t.Errorf("expected no error (os.Mkdir) but got one: %v", err)
	}

	mappingFileNameInDateDir2 := filepath.Join(dateDir2, "mapping.yaml")
	if err = ioutil.WriteFile(mappingFileNameInDateDir2, []byte(config2Str), 0644); err != nil {
		t.Errorf("expected no error (configFile.Write) but got one: %v", err)
	}

	if err := os.Remove(filepath.Join(testDir, "..data")); err != nil {
		t.Errorf("failed to unlink: %s", err)
	}
	if err := os.Symlink(date2, filepath.Join(testDir, "..data")); err != nil {
		t.Errorf("Failed to create sylink: %s", err)
	}

	expected = &Configuration{
		Secrets: []MirrorConfig{
			{
				From: SecretLocation{Namespace: "source-namespace-1", Name: "dev-secret-1"},
				To:   SecretLocation{Namespace: "target-namespace-2", Name: "prod-secret-1"},
			},
			{
				From: SecretLocation{Namespace: "source-namespace-3", Name: "dev-secret-1"},
				To:   SecretLocation{Namespace: "target-namespace-4", Name: "prod-secret-1"},
			},
		},
	}

	err = wait.Poll(1*time.Second, 10*time.Second,
		func() (bool, error) {
			result = unitUnderTest.config()
			if !reflect.DeepEqual(expected, result) {
				return false, nil
			}
			return true, nil
		})
	if err != nil {
		t.Errorf("expected no error (wait.Poll) but got one: %v", err)
	}
}
