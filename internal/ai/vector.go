// Package ai holds the building blocks of Apexx's retrieval: vectors
// quantized to int8, an in-memory index per store, embedding providers and
// the evaluation metrics every AI phase is measured with.
//
// Vectors live in Postgres (ai_chunks, with row level security) as the
// source of truth, and each node keeps a quantized copy in memory, like the
// rest of the projection: no separate vector service to keep in step.
package ai

import (
	"container/heap"
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
	"sync"
)

// Quantize turns a vector into int8 after normalizing it to length 1, so the
// dot product of two quantized vectors orders like their cosine similarity.
func Quantize(v []float32) []int8 {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	q := make([]int8, len(v))
	if n == 0 {
		return q
	}
	inv := 127 / math.Sqrt(n)
	for i, x := range v {
		f := math.Round(float64(x) * inv)
		if f > 127 {
			f = 127
		} else if f < -127 {
			f = -127
		}
		q[i] = int8(f)
	}
	return q
}

// Dot is the dot product of two int8 vectors of the same length.
func Dot(a, b []int8) int32 {
	var s0, s1, s2, s3 int32
	n := len(a)
	b = b[:n]
	i := 0
	for ; i+4 <= n; i += 4 {
		s0 += int32(a[i]) * int32(b[i])
		s1 += int32(a[i+1]) * int32(b[i+1])
		s2 += int32(a[i+2]) * int32(b[i+2])
		s3 += int32(a[i+3]) * int32(b[i+3])
	}
	for ; i < n; i++ {
		s0 += int32(a[i]) * int32(b[i])
	}
	return s0 + s1 + s2 + s3
}

// Similarity maps a dot product of two quantized unit vectors to [-1, 1].
func Similarity(d int32) float64 { return float64(d) / (127 * 127) }

// Encode packs an int8 vector for storage (bytea).
func Encode(q []int8) []byte {
	b := make([]byte, len(q))
	for i, x := range q {
		b[i] = byte(x)
	}
	return b
}

// Decode reverses Encode.
func Decode(b []byte) []int8 {
	q := make([]int8, len(b))
	for i, x := range b {
		q[i] = int8(x)
	}
	return q
}

// Float32Bytes and BytesFloat32 serialize float vectors (little endian), for
// tests and providers that return raw floats.
func Float32Bytes(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

// ErrDim means a vector does not have the index's dimension (another model).
var ErrDim = errors.New("ai: vector dimension does not match the index")

// Hit is one result of a vector search.
type Hit struct {
	ID    string
	Score float64 // cosine similarity, -1..1
}

// Index is the in-memory vector index of one store: one vector per id
// (products) kept in a flat array, scanned exactly. Exact scan over int8 is
// a few milliseconds for 50 000 vectors and never misses a result, which an
// approximate graph index can; the evaluation decides if that ever changes.
type Index struct {
	mu    sync.RWMutex
	dim   int
	model string
	ids   []string
	hash  [][]byte // source hash per id: unchanged sources are not embedded again
	data  []int8   // len(ids) * dim
	sign  []uint64 // len(ids) * words: one bit per dimension (x > 0)
	words int
	pos   map[string]int
}

// NewIndex returns an empty index for vectors of dim dimensions made by model.
func NewIndex(model string, dim int) *Index {
	return &Index{dim: dim, model: model, words: (dim + 63) / 64, pos: map[string]int{}}
}

// Model and Dim describe the vectors the index holds.
func (x *Index) Model() string { return x.model }
func (x *Index) Dim() int      { return x.dim }

// Len is the number of vectors.
func (x *Index) Len() int {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return len(x.ids)
}

// Bytes is the memory the vectors take.
func (x *Index) Bytes() int {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return len(x.data)
}

// Hash returns the source hash stored with id.
func (x *Index) Hash(id string) ([]byte, bool) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	i, ok := x.pos[id]
	if !ok {
		return nil, false
	}
	return x.hash[i], true
}

// Put adds or replaces the vector of id.
func (x *Index) Put(id string, q []int8, hash []byte) error {
	if len(q) != x.dim {
		return ErrDim
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	sb := signBits(q, x.words)
	if i, ok := x.pos[id]; ok {
		copy(x.data[i*x.dim:(i+1)*x.dim], q)
		copy(x.sign[i*x.words:(i+1)*x.words], sb)
		x.hash[i] = hash
		return nil
	}
	x.pos[id] = len(x.ids)
	x.ids = append(x.ids, id)
	x.hash = append(x.hash, hash)
	x.data = append(x.data, q...)
	x.sign = append(x.sign, sb...)
	return nil
}

// Delete removes id (the last vector takes its place).
func (x *Index) Delete(id string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	i, ok := x.pos[id]
	if !ok {
		return
	}
	last := len(x.ids) - 1
	if i != last {
		x.ids[i] = x.ids[last]
		x.hash[i] = x.hash[last]
		copy(x.data[i*x.dim:(i+1)*x.dim], x.data[last*x.dim:(last+1)*x.dim])
		copy(x.sign[i*x.words:(i+1)*x.words], x.sign[last*x.words:(last+1)*x.words])
		x.pos[x.ids[i]] = i
	}
	x.ids = x.ids[:last]
	x.hash = x.hash[:last]
	x.data = x.data[:last*x.dim]
	x.sign = x.sign[:last*x.words]
	delete(x.pos, id)
}

// Vector returns a copy of the vector of id.
func (x *Index) Vector(id string) ([]int8, bool) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	i, ok := x.pos[id]
	if !ok {
		return nil, false
	}
	return append([]int8(nil), x.data[i*x.dim:(i+1)*x.dim]...), true
}

// Centroid is the normalized mean of the vectors of ids (those present).
func (x *Index) Centroid(ids []string) ([]int8, bool) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	sum := make([]float32, x.dim)
	n := 0
	for _, id := range ids {
		i, ok := x.pos[id]
		if !ok {
			continue
		}
		n++
		for j, v := range x.data[i*x.dim : (i+1)*x.dim] {
			sum[j] += float32(v)
		}
	}
	if n == 0 {
		return nil, false
	}
	return Quantize(sum), true
}

// IDs lists every id in the index.
func (x *Index) IDs() []string {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return append([]string(nil), x.ids...)
}

// Exact returns the k ids most similar to q, best first, scanning every
// vector. keep, when not nil, filters ids (a product hidden, out of the
// channel…). Search gives the same answer much faster on large stores.
func (x *Index) Exact(q []int8, k int, keep func(id string) bool) ([]Hit, error) {
	if len(q) != x.dim {
		return nil, ErrDim
	}
	if k <= 0 {
		return nil, nil
	}
	x.mu.RLock()
	defer x.mu.RUnlock()
	h := make(minHeap, 0, k+1)
	d := x.dim
	for i, id := range x.ids {
		s := Dot(q, x.data[i*d:(i+1)*d])
		if len(h) == k && s <= h[0].s {
			continue
		}
		if keep != nil && !keep(id) {
			continue
		}
		if len(h) < k {
			heap.Push(&h, scored{i, s})
		} else {
			h[0] = scored{i, s}
			heap.Fix(&h, 0)
		}
	}
	out := make([]Hit, len(h))
	for j := len(h) - 1; j >= 0; j-- {
		e := heap.Pop(&h).(scored)
		out[j] = Hit{ID: x.ids[e.i], Score: Similarity(e.s)}
	}
	return out, nil
}

// exactBelow is the store size under which Search scans every vector: below
// it the full int8 scan already takes well under a millisecond.
const exactBelow = 4000

// Search returns the k ids most similar to q, best first, in two passes:
//  1. coarse: one bit per dimension (the sign), compared with popcount
//     (Hamming distance), 64 dimensions per instruction, over every vector;
//  2. exact: the int8 dot product only over the closest candidates
//     (at least 400, or 10 per requested result).
//
// Small stores go straight to the exact scan.
func (x *Index) Search(q []int8, k int, keep func(id string) bool) ([]Hit, error) {
	if len(q) != x.dim {
		return nil, ErrDim
	}
	if k <= 0 {
		return nil, nil
	}
	x.mu.RLock()
	n := len(x.ids)
	x.mu.RUnlock()
	if n <= exactBelow {
		return x.Exact(q, k, keep)
	}
	want := max(400, 10*k)
	qs := signBits(q, x.words)
	x.mu.RLock()
	defer x.mu.RUnlock()
	n = len(x.ids)
	dist := make([]uint16, n)
	hist := make([]int, x.dim+1)
	w := x.words
	for i := 0; i < n; i++ {
		var d int
		row := x.sign[i*w : (i+1)*w]
		for j, b := range row {
			d += bits.OnesCount64(b ^ qs[j])
		}
		dist[i] = uint16(d)
		hist[d]++
	}
	// The distance below which there are at least want vectors.
	cut, acc := 0, 0
	for ; cut <= x.dim; cut++ {
		acc += hist[cut]
		if acc >= want {
			break
		}
	}
	h := make(minHeap, 0, k+1)
	d := x.dim
	for i := 0; i < n; i++ {
		if int(dist[i]) > cut {
			continue
		}
		id := x.ids[i]
		s := Dot(q, x.data[i*d:(i+1)*d])
		if len(h) == k && s <= h[0].s {
			continue
		}
		if keep != nil && !keep(id) {
			continue
		}
		if len(h) < k {
			heap.Push(&h, scored{i, s})
		} else {
			h[0] = scored{i, s}
			heap.Fix(&h, 0)
		}
	}
	out := make([]Hit, len(h))
	for j := len(h) - 1; j >= 0; j-- {
		e := heap.Pop(&h).(scored)
		out[j] = Hit{ID: x.ids[e.i], Score: Similarity(e.s)}
	}
	return out, nil
}

func signBits(q []int8, words int) []uint64 {
	b := make([]uint64, words)
	for i, v := range q {
		if v > 0 {
			b[i/64] |= 1 << (uint(i) % 64)
		}
	}
	return b
}

type scored struct {
	i int
	s int32
}

type minHeap []scored

func (h minHeap) Len() int { return len(h) }
func (h minHeap) Less(i, j int) bool {
	if h[i].s != h[j].s {
		return h[i].s < h[j].s
	}
	return h[i].i > h[j].i // ties: earlier ids rank higher, so results are stable
}
func (h minHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(v any)   { *h = append(*h, v.(scored)) }
func (h *minHeap) Pop() any {
	o := *h
	v := o[len(o)-1]
	*h = o[:len(o)-1]
	return v
}
