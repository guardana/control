package classify_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/guardana/control/internal/classify"
)

func TestAPointerIsCheckedAgainstRFC6901AndTheBound(t *testing.T) {
	eight := strings.Repeat("/a", 8)
	token64 := "/" + strings.Repeat("t", 64)
	for _, p := range []string{"", "/", "/a", "/a~0b~1c", eight, token64, "/0/x"} {
		if err := classify.CheckPointer(p); err != nil {
			t.Errorf("CheckPointer(%q) = %v, want accepted", p, err)
		}
	}
	for _, p := range []string{"a", "a/b", eight + "/a", token64 + "t", "/a~", "/a~2", "/~"} {
		if err := classify.CheckPointer(p); !errors.Is(err, classify.ErrPointer) {
			t.Errorf("CheckPointer(%q) = %v, want ErrPointer", p, err)
		}
	}
	if classify.MaxPointerTokens != 8 || classify.MaxPointerTokenSize != 64 {
		t.Errorf("the bounds are %d tokens of %d bytes; the cases above are written for 8 of 64",
			classify.MaxPointerTokens, classify.MaxPointerTokenSize)
	}
}
