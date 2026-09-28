package switcher

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
)

type modelEntry struct {
	ID     string `json:"id"`
	Status struct {
		Value string `json:"value"`
	} `json:"status"`
}

// modelList is one router's answer to GET /models or GET /v1/models.
type modelList struct {
	b    *backend
	body map[string]json.RawMessage
}

// serveModelList merges the model lists of all routers. Each model appears
// once, from the router that owns it.
func (s *Switcher) serveModelList(w http.ResponseWriter, r *http.Request) {
	lists := s.fetchLists(r.Context(), r.URL.RequestURI())
	if len(lists) == 0 || lists[0].b != s.def {
		// Without the default router's list there is nothing sensible to merge.
		s.def.serve(w, r)
		return
	}
	out := lists[0].body
	data, err := json.Marshal(s.mergeLists(lists))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out["data"] = data
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// fetchLists asks every available router for its list, in s.all() order.
func (s *Switcher) fetchLists(ctx context.Context, uri string) []modelList {
	backends := s.all()
	results := make([]*modelList, len(backends))
	var wg sync.WaitGroup
	for i, b := range backends {
		if !b.available() {
			continue
		}
		wg.Add(1)
		go func(i int, b *backend) {
			defer wg.Done()
			var body map[string]json.RawMessage
			if s.getJSON(ctx, b.url(uri), &body) == nil && body != nil {
				results[i] = &modelList{b: b, body: body}
			}
		}(i, b)
	}
	wg.Wait()
	var lists []modelList
	for _, l := range results {
		if l != nil {
			lists = append(lists, *l)
		}
	}
	return lists
}

// mergeLists keeps each model from the router that owns it.
func (s *Switcher) mergeLists(lists []modelList) []json.RawMessage {
	merged := []json.RawMessage{}
	for _, l := range lists {
		var items []json.RawMessage
		if json.Unmarshal(l.body["data"], &items) != nil {
			continue
		}
		for _, it := range items {
			var e modelEntry
			if json.Unmarshal(it, &e) == nil && s.owns(l.b, e.ID) {
				merged = append(merged, it)
			}
		}
	}
	return merged
}

func (s *Switcher) loadedModels(ctx context.Context, b *backend) []string {
	var list struct {
		Data []modelEntry `json:"data"`
	}
	if err := s.getJSON(ctx, b.url("/models"), &list); err != nil {
		return nil
	}
	var ids []string
	for _, m := range list.Data {
		if m.Status.Value == "loaded" || m.Status.Value == "loading" {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

// broadcast sends a request to every router and returns the default
// router's answer. Used for "GET /models?reload=1".
func (s *Switcher) broadcast(w http.ResponseWriter, r *http.Request) {
	for _, b := range s.all()[1:] {
		if b.available() {
			if err := s.get(r.Context(), b.url(r.URL.RequestURI())); err != nil {
				s.opt.Log("reload " + b.name + ": " + err.Error())
			}
		}
	}
	s.def.serve(w, r)
}
