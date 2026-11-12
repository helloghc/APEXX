package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Kind tells a provider whether it embeds stored text or a search query;
// some models embed them differently.
type Kind string

const (
	Document Kind = "document"
	Query    Kind = "query"
)

// Usage is what one embedding call cost.
type Usage struct {
	Tokens     int
	CostMicros int64 // millionths of a US dollar
}

// Embedder turns texts into vectors.
type Embedder interface {
	Model() string
	Dim() int
	Embed(ctx context.Context, texts []string, kind Kind) ([][]float32, Usage, error)
}

// ---------- local: subword hashing, no network ----------

// Local embeds with hashed words, stems, word pairs and character trigrams
// (feature hashing). It needs no network and costs nothing, so the store
// always has vectors: it catches plurals, typos, word order and partial
// words ("zapatilla" ~ "zapatillas", "imperme" ~ "impermeable"). It does
// not know that "tenis" and "calzado deportivo" mean the same; a semantic
// provider (Voyage) does, and the evaluation measures both.
type Local struct{ D int }

// NewLocal returns the local embedder with dim dimensions (384 by default).
func NewLocal(dim int) *Local {
	if dim <= 0 {
		dim = 384
	}
	return &Local{D: dim}
}

func (l *Local) Model() string { return fmt.Sprintf("apexx-hash-v2-%d", l.D) }
func (l *Local) Dim() int      { return l.D }

func (l *Local) Embed(_ context.Context, texts []string, _ Kind) ([][]float32, Usage, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = l.vector(t)
	}
	return out, Usage{}, nil
}

func (l *Local) vector(text string) []float32 {
	v := make([]float32, l.D)
	add := func(feature string, w float32) {
		h := fnv.New64a()
		_, _ = h.Write([]byte(feature))
		s := h.Sum64()
		i := int(s % uint64(l.D))
		if s>>63 == 1 {
			w = -w
		}
		v[i] += w
	}
	words := Words(text)
	for j, w := range words {
		if stopwords[w] {
			// Function words say little about a product: "para", "de", "la"
			// would make "algo para la lluvia" close to "para concreto".
			add("w:"+w, 0.05)
			continue
		}
		add("w:"+w, 1)
		if st := Stem(w); st != w {
			add("w:"+st, 0.8)
		}
		add("s:"+Stem(w), 0.6)
		if j > 0 {
			add("b:"+Stem(words[j-1])+" "+Stem(w), 0.5)
		}
		g := "^" + Stem(w) + "$"
		r := []rune(g)
		for k := 0; k+3 <= len(r); k++ {
			add("g:"+string(r[k:k+3]), 0.35)
		}
	}
	// Words that repeat (title and description both name the thing) weigh
	// more, but sublinearly.
	for i, x := range v {
		if x > 0 {
			v[i] = float32(math.Sqrt(float64(x)))
		} else if x < 0 {
			v[i] = -float32(math.Sqrt(float64(-x)))
		}
	}
	return v
}

// stopwords are Spanish and Portuguese function words (folded).
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a al algo algun alguna algunas alguno algunos ante antes aqui asi aun bajo cada casi como con contra cual cuales cuando de del desde donde dos e el ella ellas ellos en entre era es esa esas ese eso esos esta estas este esto estos fue ha hay hasta la las le les lo los mas me mi mis muy ni no nos o otra otras otro otros para pero poco por porque que quien se sea segun ser si sin sobre solo son su sus tambien tan tanto te tiene tu tus un una unas uno unos y ya yo quiero busco necesito
		da das do dos na nas no nos num numa o os pra pelo pela um uma uns umas com sem sobre`) {
		stopwords[w] = true
	}
}

// Root is a light Spanish stem for matching words of one family:
// devolver, devolución and devolverlo share "devol"; lavo, lava and lavar
// share "lav". It strips an attached pronoun and the usual endings, then
// keeps at most five letters.
func Root(w string) string {
	if r, ok := irregular[w]; ok {
		return r
	}
	if len(w) > 6 {
		for _, e := range []string{"selo", "sela", "los", "las", "lo", "la", "le", "les", "se", "me", "te"} {
			if strings.HasSuffix(w, e) && (strings.HasSuffix(w[:len(w)-len(e)], "ar") || strings.HasSuffix(w[:len(w)-len(e)], "er") || strings.HasSuffix(w[:len(w)-len(e)], "ir")) {
				w = w[:len(w)-len(e)]
				break
			}
		}
	}
	for _, e := range []string{"aciones", "iciones", "ciones", "acion", "icion", "cion", "siones", "sion", "mente", "amos", "emos", "imos", "ando", "iendo", "aron", "ieron", "ado", "ido", "ada", "ida", "ados", "idos", "ar", "er", "ir", "as", "es", "os", "an", "en", "a", "e", "o", "s"} {
		if strings.HasSuffix(w, e) && len(w)-len(e) >= 3 {
			w = w[:len(w)-len(e)]
			break
		}
	}
	if r := []rune(w); len(r) > 5 {
		w = string(r[:5])
	}
	return w
}

// irregular maps the forms of frequent irregular verbs to one root:
// "¿cuántos días tengo?" must meet "tienes 30 días".
var irregular = map[string]string{}

func init() {
	for root, forms := range map[string]string{
		"tener": "tengo tienes tiene tienen tenemos tenga tengan tuve tuvo tendra tendran tener tenia",
		"poder": "puedo puedes puede pueden podemos pueda puedan podra podran poder podria",
		"hacer": "hago haces hace hacen hacemos haga hagan hizo hara haran hacer",
		"decir": "digo dices dice dicen decimos diga digan dijo decir",
		"venir": "vengo vienes viene vienen venimos venga vengan vino vendra venir",
		"poner": "pongo pones pone ponen ponemos ponga pongan puso pondra poner",
		"salir": "salgo sales sale salen salimos salga salgan salio saldra salir",
		"traer": "traigo traes trae traen traemos traiga traigan trajo traera traer",
	} {
		for _, f := range strings.Fields(forms) {
			irregular[f] = root
		}
	}
}

// IsStopword reports whether w (folded) is a function word.
func IsStopword(w string) bool { return stopwords[w] }

// Words folds text (lowercase, no accents) and splits it into words.
func Words(s string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			b.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}

// Stem strips the most common Spanish and Portuguese plural endings, enough
// for "camisas" ~ "camisa" and "pantalones" ~ "pantalon".
func Stem(w string) string {
	n := len(w)
	switch {
	case n > 4 && strings.HasSuffix(w, "ces"):
		return w[:n-3] + "z" // luces -> luz
	case n > 4 && strings.HasSuffix(w, "es") && !strings.ContainsAny(w[n-3:n-2], "aeiou"):
		return w[:n-2] // pantalones -> pantalon
	case n > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
		return w[:n-1]
	}
	return w
}

// ---------- Voyage AI: semantic embeddings over HTTPS ----------

// Voyage calls the Voyage AI embeddings API. The key is sent by the
// platform's network proxy or comes from VOYAGE_API_KEY; it is never stored
// by Apexx.
type Voyage struct {
	Key        string
	ModelName  string // e.g. voyage-3.5-lite
	D          int    // output dimension (256, 512, 1024)
	URL        string // https://api.voyageai.com/v1/embeddings
	PricePerM  int64  // micro-dollars per million tokens
	HTTPClient *http.Client
}

func (v *Voyage) Model() string { return fmt.Sprintf("%s-%d", v.ModelName, v.D) }
func (v *Voyage) Dim() int      { return v.D }

func (v *Voyage) Embed(ctx context.Context, texts []string, kind Kind) ([][]float32, Usage, error) {
	if len(texts) == 0 {
		return nil, Usage{}, nil
	}
	body, _ := json.Marshal(map[string]any{"input": texts, "model": v.ModelName, "input_type": string(kind), "output_dimension": v.D})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.URL, bytes.NewReader(body))
	if err != nil {
		return nil, Usage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if v.Key != "" {
		req.Header.Set("Authorization", "Bearer "+v.Key)
	}
	c := v.HTTPClient
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, Usage{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, Usage{}, fmt.Errorf("voyage: %s: %s", resp.Status, truncate(string(raw), 200))
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, Usage{}, err
	}
	vecs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vecs) || len(d.Embedding) != v.D {
			return nil, Usage{}, errors.New("voyage: unexpected response shape")
		}
		vecs[d.Index] = d.Embedding
	}
	for _, x := range vecs {
		if x == nil {
			return nil, Usage{}, errors.New("voyage: missing embeddings")
		}
	}
	u := Usage{Tokens: out.Usage.TotalTokens, CostMicros: int64(out.Usage.TotalTokens) * v.PricePerM / 1_000_000}
	return vecs, u, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
