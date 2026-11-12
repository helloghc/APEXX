package catalog

import (
	"sort"
	"strings"
	"sync"
)

// Word index: which products contain each search word. A query used to test
// every word of every product (50 000 products, about 750 000 words) with
// typo tolerance; now each distinct word is tested once per query token, and
// only words of a length that can match are tested at all. Results are the
// same as matchesAll: the index only narrows which products to look at.

type wordIndex struct {
	posts map[string]map[string]struct{} // word -> product ids
	byLen map[int]map[string]struct{}    // word length -> words with a letter
	// nums holds the words made only of digits (sizes, models, years): a
	// catalog can have tens of thousands. They match by prefix only, found
	// by binary search in numSorted (rebuilt after changes, on first use).
	nums      map[string]struct{}
	numSorted []string
	numDirty  bool
	numMu     sync.Mutex
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func (x *wordIndex) add(id string, hay []string) {
	if x.posts == nil {
		x.posts, x.byLen, x.nums = map[string]map[string]struct{}{}, map[int]map[string]struct{}{}, map[string]struct{}{}
	}
	for _, w := range hay {
		ids := x.posts[w]
		if ids == nil {
			ids = map[string]struct{}{}
			x.posts[w] = ids
			if isDigits(w) {
				x.nums[w] = struct{}{}
				x.numDirty = true
			} else {
				if x.byLen[len(w)] == nil {
					x.byLen[len(w)] = map[string]struct{}{}
				}
				x.byLen[len(w)][w] = struct{}{}
			}
		}
		ids[id] = struct{}{}
	}
}

func (x *wordIndex) remove(id string, hay []string) {
	for _, w := range hay {
		ids := x.posts[w]
		if ids == nil {
			continue
		}
		delete(ids, id)
		if len(ids) == 0 {
			delete(x.posts, w)
			if isDigits(w) {
				delete(x.nums, w)
				x.numDirty = true
			} else {
				delete(x.byLen[len(w)], w)
			}
		}
	}
}

// matchingWords returns the indexed words a query token matches, as
// tokenMatches decides: a prefix, or one typo for tokens of four or more
// letters. A word shorter than the token by two or more cannot match.
func (x *wordIndex) matchingWords(tok string) map[string]bool {
	out := map[string]bool{}
	for n, ws := range x.byLen {
		if n < len(tok)-1 {
			continue
		}
		for w := range ws {
			if tokenMatches(tok, []string{w}) {
				out[w] = true
			}
		}
	}
	// Words of digits only: a numeric token matches them by prefix, and a
	// token with letters only when it is one edit away from a number
	// ("12a4" ~ "1234"), which needs at most one non-digit.
	nonDigits := 0
	for i := 0; i < len(tok); i++ {
		if tok[i] < '0' || tok[i] > '9' {
			nonDigits++
		}
	}
	switch {
	case nonDigits == 0:
		ns := x.sortedNums()
		for i := sort.SearchStrings(ns, tok); i < len(ns) && strings.HasPrefix(ns[i], tok); i++ {
			out[ns[i]] = true
		}
	case nonDigits == 1:
		for w := range x.nums {
			if tokenMatches(tok, []string{w}) {
				out[w] = true
			}
		}
	}
	return out
}

// sortedNums returns the numeric words in order. Callers hold the store's
// read lock, so the rebuild is guarded by its own lock.
func (x *wordIndex) sortedNums() []string {
	x.numMu.Lock()
	defer x.numMu.Unlock()
	if x.numDirty || x.numSorted == nil {
		x.numSorted = x.numSorted[:0]
		for w := range x.nums {
			x.numSorted = append(x.numSorted, w)
		}
		sort.Strings(x.numSorted)
		x.numDirty = false
	}
	return x.numSorted
}

// Matching returns the ids of products whose words match every token of q
// (any status), exactly the products matchesAll accepts. It returns nil when
// q has no tokens, meaning "every product".
func (s *Store) Matching(q string) map[string]bool {
	qs := words(q)
	if len(qs) == 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	type tok struct {
		words map[string]bool
		size  int
	}
	ts := make([]tok, len(qs))
	for i, t := range qs {
		ts[i].words = s.index.matchingWords(t)
		for w := range ts[i].words {
			ts[i].size += len(s.index.posts[w])
		}
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].size < ts[j].size })
	out := map[string]bool{}
	for w := range ts[0].words {
		for id := range s.index.posts[w] {
			out[id] = true
		}
	}
	for _, t := range ts[1:] {
		for id := range out {
			hit := false
			if p := s.byID[id]; p != nil {
				for _, w := range p.hay {
					if t.words[w] {
						hit = true
						break
					}
				}
			}
			if !hit {
				delete(out, id)
			}
		}
	}
	return out
}
