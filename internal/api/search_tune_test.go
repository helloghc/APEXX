package api

import (
	"os"
	"testing"

	"github.com/helloghc/apexx/internal/ai"
	"github.com/helloghc/apexx/internal/ai/evalset"
)

// TestTuneSemanticFloors explores the floors (APEXX_TUNE=1 only).
func TestTuneSemanticFloors(t *testing.T) {
	if os.Getenv("APEXX_TUNE") == "" {
		t.Skip("APEXX_TUNE not set")
	}
	ten, ids := evalStore(t)
	hyb, _ := hybridRank(ten, ids, ai.NewLocal(384))
	f0, r0 := semanticFloor, semanticRel
	defer func() { semanticFloor, semanticRel = f0, r0 }()
	for _, f := range []float64{0.16, 0.18, 0.20, 0.22, 0.24} {
		for _, r := range []float64{0, 0.3, 0.4, 0.5} {
			semanticFloor, semanticRel = f, r
			s := evalset.Evaluate(hyb, ai.NDCG, ai.Recall)
			junk := 0
			for _, q := range []string{"zzqxv", "kkkjjj wwq", "xptlqz", "asdfgh", "qwerty uiop"} {
				junk += len(hyb(q))
			}
			t.Logf("floor %.2f rel %.1f  nDCG %.3f  recall %.3f  empty %.0f%%  junk %d  %v", f, r, s.NDCG10, s.Recall50, s.Empty*100, junk, round(s.ByKind))
		}
	}
}
