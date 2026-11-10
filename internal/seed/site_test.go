package seed

import (
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/builder"
)

func TestHomeTemplateRendersClean(t *testing.T) {
	lib, err := builder.NewLibrary(builder.DefaultSections()...)
	if err != nil {
		t.Fatal(err)
	}
	s := builder.NewSite("s", lib, nil, HomeTemplate())
	html, rep, err := s.Preview("index")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "ZgotmplZ") {
		t.Fatal("home template renders a sanitized (broken) value")
	}
	for _, i := range rep.Issues {
		if i.Blocking {
			t.Errorf("blocking issue %s %s", i.Path, i.Message)
		}
	}
}
