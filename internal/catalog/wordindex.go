package catalog

import "sort"

// Word index: which products contain each search word. A query used to test
// every word of every product (50 000 products, about 750 000 words) with
// typo tolerance; now each distinct word is tested once per query token, and
// only words of a length that can match are tested at all. Results are the
// same as matchesAll: the index only narrows which products to look at.

type wordIndex struct {
	posts map[string]map[string]struct{} // word -> product ids
	byLen map[int]map[string]struct{}    // word length -> words
}

func (x *wordIndex) add(id string, hay []string) {
	if x.posts == nil {
		x.posts, x.byLen = map[string]map[string]struct{}{}, map[int]map[string]struct{}{}
	}
	for _, w := range hay {
		ids := x.posts[w]
		if ids == nil {
			ids = map[string]struct{}{}
			x.posts[w] = ids
			if x.byLen[len(w)] == nil {
				x.byLen[len(w)] = map[string]struct{}{}
			}
			x.byLen[len(w)][w] = struct{}{}
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
			delete(x.byLen[len(w)], w)
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
	return out
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
