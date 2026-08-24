package build

import (
	"os"
	"testing"
)

func TestCopyArtifact(t *testing.T) {
	if err := CopyArtifact("/Users/devoncorey/Documents/huigojo_project", "huigojo-applet/*", "/Users/devoncorey/Documents/temp_copy", os.Stdin); err != nil {
		t.Fatal(err)
	}
	t.Log("拷贝完成")
}
