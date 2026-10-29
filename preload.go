package orm

import (
	"cmp"
	"fmt"
	"slices"
)

// preload is a relation to load along with the rows of a scope, and the scope its rows are read by.
type preload struct {
	relation string
	sub      subscope
}

// subscope is an Of of whatever model a relation reaches.
type subscope interface {
	join(other subscope) subscope
}

func (q Of[M]) join(other subscope) subscope {
	o, ok := other.(Of[M])
	if !ok {
		panic(fmt.Sprintf("orm: a relation preloaded as both %T and %T", q, other))
	}
	return Join(q, o)
}

// Preload has the rows of relation loaded along with those of q, read by sub. Preloading a relation again joins the
// two scopes as Join does. A limit or an offset in sub would count the related rows of every row together, not those
// of each, so it is an error.
func Preload[M, C any](q Of[M], relation string, sub Of[C]) Of[M] {
	if sub.hasLimit || sub.hasOffset || sub.seeking {
		q.err = cmp.Or(q.err, fmt.Errorf(
			"orm: %s keeps only some of the rows it loads, which would count those of every row together", relation))
	}
	q.err = cmp.Or(q.err, sub.err)
	q.preloads = addPreload(q.preloads, preload{relation, sub})
	return q
}

// Preloaded returns the scope the rows of relation are read by, and whether q loads them at all.
func Preloaded[C, M any](q Of[M], relation string) (Of[C], bool) {
	for _, p := range q.preloads {
		if p.relation != relation {
			continue
		}
		sub, ok := p.sub.(Of[C])
		if !ok {
			panic(fmt.Sprintf("orm: %s is preloaded as %T, not %T", relation, p.sub, sub))
		}
		return sub, true
	}
	return Of[C]{}, false
}

// addPreload returns ps with p added, joined with the scope of its relation if ps has one already.
func addPreload(ps []preload, p preload) []preload {
	i := slices.IndexFunc(ps, func(q preload) bool { return q.relation == p.relation })
	if i < 0 {
		return append(slices.Clip(ps), p)
	}
	ps = slices.Clone(ps)
	ps[i].sub = ps[i].sub.join(p.sub)
	return ps
}
