package new

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// GoModFile ..
const GoModFile = "go.mod"

//Create ..
func Create(filename string) (*os.File, error) {
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return os.Create(filename)
	}
	return nil, fmt.Errorf("%s already exist", filename)
}

//MkdirAll ..
func MkdirAll(dirs ...string) error {
	dir := filepath.Join(dirs...)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return os.MkdirAll(dir, os.ModePerm)
	}
	return nil
}

// func readpkg(name string) (string, error) {
// 	file, err := pkger.Open(name)
// 	if err != nil {
// 		return "", err
// 	}
// 	defer file.Close()
// 	content, err := ioutil.ReadAll(file)
// 	if err != nil {
// 		return "", err
// 	}
// 	return string(content), nil
// }

//GetModPath ..
func GetModPath() string {
	b, err := run("go", "env", "GOMOD")
	if err != nil {
		return ""
	}
	return string(b)
}

//get a run result
func run(n string, args ...string) ([]byte, error) {
	c := exec.Command(n, args...)
	bb := &bytes.Buffer{}
	ebb := &bytes.Buffer{}
	c.Stdout = bb
	c.Stderr = ebb
	err := c.Run()
	if err != nil {
		return nil, fmt.Errorf("%v %w: %s", c.Args, err, ebb)
	}
	return bb.Bytes(), nil
}
