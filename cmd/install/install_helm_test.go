package install

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func ExecuteTestHelmCommand(args []string) ([]byte, error) {
	// append helm to args
	args = append([]string{"helm"}, args...)
	cmd := NewCommand()
	cmd.SetArgs(args)
	b := bytes.NewBufferString("")
	cmd.SetOut(b)
	err := cmd.Execute()
	if err != nil {
		return []byte{}, err
	}
	return io.ReadAll(b)
}

func TestHelmCommand(t *testing.T) {
	// create a folder to hold test data and delete it afterwards
	tmpDir, err := os.MkdirTemp("", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	_, err = ExecuteTestHelmCommand([]string{"--output-dir", tmpDir})
	if err != nil {
		t.Fatal(err)
	}

	// check if the output directory exists
	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		t.Fatalf("Output directory %s does not exist", tmpDir)
	}

	// check if the crds directory exists ...
	for _, dir := range []string{"crds", "templates"} {
		dirPath := tmpDir + "/" + dir
		if _, err := os.Stat(dirPath); os.IsNotExist(err) {
			t.Fatalf("%s directory %s does not exist", dir, dirPath)
		}
		files, err := os.ReadDir(dirPath)
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Fatalf("%s directory is empty", dirPath)
		}
	}

	// check if the Chart.yaml file exists
	chartPath := tmpDir + "/Chart.yaml"
	if _, err := os.Stat(chartPath); os.IsNotExist(err) {
		t.Fatalf("Chart.yaml file %s does not exist", chartPath)
	}

	// check if the values.yaml file exists
	valuesPath := tmpDir + "/values.yaml"
	if _, err := os.Stat(valuesPath); os.IsNotExist(err) {
		t.Fatalf("values.yaml file %s does not exist", valuesPath)
	}

}
