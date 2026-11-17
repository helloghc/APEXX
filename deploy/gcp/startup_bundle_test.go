// Package gcp holds the deployment scripts; this test keeps the one-paste
// startup script for the browser in step with the files it carries.
package gcp

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

func TestBrowserStartupScriptIsCurrent(t *testing.T) {
	out := filepath.Join(t.TempDir(), "startup.sh")
	if b, err := exec.Command("bash", "build-browser-startup.sh", out).CombinedOutput(); err != nil {
		t.Fatalf("generator: %v\n%s", err, b)
	}
	fresh, _ := os.ReadFile(out)
	committed, err := os.ReadFile("startup-navegador.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fresh, committed) {
		t.Fatal("deploy/gcp/startup-navegador.sh está desactualizado: corre deploy/gcp/build-browser-startup.sh")
	}
	if b, err := exec.Command("bash", "-n", "startup-navegador.sh").CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s", err, b)
	}
	// Each embedded file is byte for byte the one in the repository.
	want := map[string]string{
		`"$DIR/docker-compose.yml"`:               "../../docker-compose.yml",
		`"$DIR/deploy/caddy/Caddyfile"`:           "../caddy/Caddyfile",
		`"$DIR/deploy/gcp/fetch-secrets.sh"`:      "fetch-secrets.sh",
		`"$DIR/deploy/backup/pg-backup.sh"`:       "../backup/pg-backup.sh",
		`"$DIR/deploy/backup/pg-restore.sh"`:      "../backup/pg-restore.sh",
		`/etc/google-cloud-ops-agent/config.yaml`: "ops-agent.yaml",
	}
	doc := regexp.MustCompile(`(?ms)^cat > (\S+) <<'__APEXX_FIN_DEL_ARCHIVO__'\n(.*?)^__APEXX_FIN_DEL_ARCHIVO__$`)
	found := 0
	for _, m := range doc.FindAllSubmatch(committed, -1) {
		src, ok := want[string(m[1])]
		if !ok {
			t.Errorf("archivo inesperado: %s", m[1])
			continue
		}
		orig, _ := os.ReadFile(src)
		if !bytes.Equal(m[2], orig) {
			t.Errorf("%s no es igual a %s", m[1], src)
		}
		found++
	}
	if found != len(want) {
		t.Fatalf("el script lleva %d de %d archivos", found, len(want))
	}
}
