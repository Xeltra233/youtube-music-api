package session

import (
	"strings"

	"github.com/xeltra/ytmusic-bridge/internal/matching"
	"github.com/xeltra/ytmusic-bridge/internal/search"
)

// SelectRequest ????????
//
// ????VideoID > Index > Name?? docs/BOT-INTEGRATION.md ????
// ?? Index/Name ? SessionID ???VideoID ????? session?
type SelectRequest struct {
	SessionID string
	// Index ? 1-based?0 ?????????????
	Index int
	Name  string
	// VideoID ?????????? session / index / name?
	VideoID string
}

// Select ????? session?? video_id????????
//
// ?????
//   - BadRequestError / ErrBadRequest????????? session_id?index < 0
//   - NotFoundError / ErrNotFound?session ????index ???name ???
//   - GoneError / ErrGone?session ??
//   - AmbiguousError / ErrAmbiguous?name ??????
func (s *Store) Select(req SelectRequest) (Selection, error) {
	if s == nil {
		return Selection{}, &BadRequestError{Reason: "nil store"}
	}

	videoID := strings.TrimSpace(req.VideoID)
	if videoID != "" {
		// video_id ????? session?????? VideoID?????????
		return Selection{
			Item: search.Item{
				VideoID: videoID,
				Artists: []string{},
			},
			FromSession: false,
		}, nil
	}

	sessionID := strings.TrimSpace(req.SessionID)
	name := strings.TrimSpace(req.Name)
	index := req.Index

	// ????? index ? name?
	if index == 0 && name == "" {
		return Selection{}, &BadRequestError{Reason: "must provide video_id, index, or name"}
	}
	if index < 0 {
		return Selection{}, &BadRequestError{Reason: "index must be >= 1"}
	}
	if sessionID == "" {
		return Selection{}, &BadRequestError{Reason: "session_id required when using index or name"}
	}

	snap, err := s.Get(sessionID)
	if err != nil {
		return Selection{}, err
	}

	// index ??? name?
	if index > 0 {
		item, err := selectByIndex(snap.Results, index)
		if err != nil {
			return Selection{}, err
		}
		return Selection{Item: item, FromSession: true, SessionID: sessionID}, nil
	}

	item, err := selectByName(snap.Results, name)
	if err != nil {
		return Selection{}, err
	}
	return Selection{Item: item, FromSession: true, SessionID: sessionID}, nil
}

func selectByIndex(items []search.Item, index int) (search.Item, error) {
	// ??? Item.Index ?????1-based ???????????????
	// ???????????????????????? Index ???????
	for _, it := range items {
		if it.Index == index {
			return cloneItem(it), nil
		}
	}
	// ???????? Index?? 1-based ?????
	if index >= 1 && index <= len(items) {
		// ????? Index ? 0??????????????Index ??????????????
		it := items[index-1]
		if it.Index == 0 {
			return cloneItem(it), nil
		}
	}
	return search.Item{}, &NotFoundError{Reason: "index out of range"}
}

func selectByName(items []search.Item, name string) (search.Item, error) {
	// 1) ????????? trim?????????
	var exact []search.Item
	for _, it := range items {
		if it.DisplayName == name {
			exact = append(exact, it)
		}
	}
	if len(exact) == 1 {
		return cloneItem(exact[0]), nil
	}
	if len(exact) > 1 {
		return search.Item{}, &AmbiguousError{Name: name, Candidates: cloneItems(exact)}
	}

	// 2) ????????? / ?? / ?? / ??????
	normName := matching.Normalize(name)
	if normName == "" {
		return search.Item{}, &NotFoundError{Reason: "name not found"}
	}
	var normHits []search.Item
	for _, it := range items {
		if matching.Normalize(it.DisplayName) == normName {
			normHits = append(normHits, it)
		}
	}
	if len(normHits) == 1 {
		return cloneItem(normHits[0]), nil
	}
	if len(normHits) > 1 {
		return search.Item{}, &AmbiguousError{Name: name, Candidates: cloneItems(normHits)}
	}

	// 3) ?????MatchScore ???????????? ? ???
	// ???? matching ????????? 0.75??????????????
	const fuzzyMin = 0.75
	bestScore := 0.0
	var best []search.Item
	for _, it := range items {
		score := matching.MatchScore(name, it.DisplayName)
		if score < fuzzyMin {
			continue
		}
		if score > bestScore {
			bestScore = score
			best = best[:0]
			best = append(best, it)
			continue
		}
		if score == bestScore && score > 0 {
			best = append(best, it)
		}
	}
	if len(best) == 1 {
		return cloneItem(best[0]), nil
	}
	if len(best) > 1 {
		return search.Item{}, &AmbiguousError{Name: name, Candidates: cloneItems(best)}
	}
	return search.Item{}, &NotFoundError{Reason: "name not found"}
}

func cloneItem(it search.Item) search.Item {
	out := it
	if it.Artists == nil {
		out.Artists = []string{}
	} else {
		out.Artists = append([]string(nil), it.Artists...)
	}
	return out
}
