package api

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Scheduled design: a frozen version goes live at its start and leaves at
// its end by itself; before that, a signed link shows any moment.
func TestScheduledDesign(t *testing.T) {
	e := setup(t)
	patch := func(heading string) {
		if c, b := e.do("PATCH", "/v1/sites/main/templates/index/draft", e.adminA, m{"ops": []m{{"op": "replace", "path": "/regions/main/0/settings/heading", "value": heading}}}); c != 200 {
			t.Fatalf("patch = %d %v", c, b)
		}
	}
	page := func(path string) (string, http.Header) {
		res, err := http.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b), res.Header
	}
	patch("Temporada regular")
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	patch("Especial de invierno")
	if st, _ := e.do("POST", "/v1/sites/main/schedules", e.adminA, m{"name": "Pasado", "startsAt": time.Now().Add(-2 * time.Hour), "endsAt": time.Now().Add(-time.Hour)}); st != 400 {
		t.Fatalf("a window in the past was accepted: %d", st)
	}
	if st, _ := e.do("POST", "/v1/sites/main/schedules", e.adminB, m{"name": "x", "startsAt": time.Now(), "endsAt": time.Now().Add(time.Hour)}); st != 404 && st != 200 && st != 201 {
		t.Fatalf("store B schedule = %d", st)
	}
	start, end := time.Now().Add(time.Hour), time.Now().Add(3*time.Hour)
	sc := e.must(201, "POST", "/v1/sites/main/schedules", e.adminA, m{"name": "Invierno", "startsAt": start, "endsAt": end})
	// The draft keeps changing; the scheduled version is frozen as it was.
	patch("Algo que todavía no se publica")
	html, h := page("/s/store-a/main/index")
	if !strings.Contains(html, "Temporada regular") || strings.Contains(html, "Especial de invierno") {
		t.Fatal("the scheduled version went live early")
	}
	if !strings.Contains(h.Get("Cache-Control"), "s-maxage=60") {
		t.Fatalf("with a pending schedule the cache must be short: %s", h.Get("Cache-Control"))
	}
	// Preview of the moment it will be live.
	at := start.Add(30 * time.Minute).UTC().Format(time.RFC3339)
	link := e.must(200, "GET", "/v1/sites/main/schedules/preview?at="+url.QueryEscape(at), e.adminA, nil)["url"].(string)
	if html, h := page(link); !strings.Contains(html, "Especial de invierno") || h.Get("Cache-Control") != "no-store" {
		t.Fatal("preview does not show the scheduled version")
	}
	if html, _ := page(strings.Replace(link, "vista=", "vista=00", 1)); !strings.Contains(html, "Temporada regular") {
		t.Fatal("a forged preview link showed the scheduled version")
	}
	if html, _ := page(strings.Replace(link, url.QueryEscape(at), url.QueryEscape(end.Add(time.Hour).UTC().Format(time.RFC3339)), 1)); !strings.Contains(html, "Temporada regular") {
		t.Fatal("a preview link was reused for another moment")
	}
	// Move the window to now: it goes live; after its end the regular site is back.
	id := sc["id"].(string)
	e.must(204, "DELETE", "/v1/sites/main/schedules/"+id, e.adminA, nil)
	patch("Especial de invierno")
	e.must(201, "POST", "/v1/sites/main/schedules", e.adminA, m{"name": "Ahora", "startsAt": time.Now().Add(-time.Minute), "endsAt": time.Now().Add(2 * time.Second)})
	if html, _ := page("/s/store-a/main/index"); !strings.Contains(html, "Especial de invierno") {
		t.Fatal("the scheduled version is not live inside its window")
	}
	time.Sleep(2500 * time.Millisecond)
	if html, _ := page("/s/store-a/main/index"); !strings.Contains(html, "Temporada regular") {
		t.Fatal("the regular site did not come back after the window")
	}
	if l := e.must(200, "GET", "/v1/sites/main/schedules", e.adminA, nil)["items"].([]any); len(l) != 1 || l[0].(map[string]any)["state"] != "terminada" {
		t.Fatalf("schedules = %v", l)
	}
}
