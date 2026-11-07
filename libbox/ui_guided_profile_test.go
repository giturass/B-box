package libbox

import (
	"os"
	"path/filepath"
	"testing"
)

// setup-core copies this test into .core/experimental/libbox. Flutter's form
// regression compares the actual profiles.save payload to this same fixture.
func TestUIGuidedProfileTemplate(t *testing.T) {
	path := filepath.Join("..", "..", "..", "flutter", "test", "fixtures", "guided_profile.json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckConfig(string(content)); err != nil {
		t.Fatalf("the profile created by the node form is not accepted by the current core: %v", err)
	}
	formatted, err := FormatConfig(string(content))
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckConfig(formatted.Value); err != nil {
		t.Fatalf("the formatted profile is not accepted by the current core: %v", err)
	}
}
