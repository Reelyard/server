package store

import (
	"context"
	"errors"
	"testing"
)

func TestViewers(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	admin, err := s.CreateFirstUser(ctx, "ana", "hash")
	if err != nil {
		t.Fatal(err)
	}
	kids := &Viewer{Name: "Kids", Titles: []string{"tt1", " tt2 ", ""}, DLNA: true, TVs: []string{"192.168.1.20"}}
	if err := s.CreateViewer(ctx, kids); err != nil {
		t.Fatal(err)
	}
	rui := &Viewer{Name: "Rui", AllTitles: true, DLNA: true}
	if err := s.CreateViewer(ctx, rui); err != nil {
		t.Fatal(err)
	}
	if kids.Slot != 1 || rui.Slot != 2 || kids.ID == admin.ID {
		t.Fatalf("slots %d %d, ids %d %d", kids.Slot, rui.Slot, kids.ID, admin.ID)
	}
	if err := s.CreateViewer(ctx, &Viewer{Name: "kids"}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("duplicate name: %v", err)
	}
	// The admin stays the first user, and viewers can't be found by a login name.
	if u, _ := s.FirstUser(ctx); u.ID != admin.ID {
		t.Fatalf("first user %d", u.ID)
	}
	if _, err := s.UserByName(ctx, "Kids"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("viewer found by name: %v", err)
	}

	got, err := s.GetViewer(ctx, kids.ID)
	if err != nil || got.Name != "Kids" || got.AllTitles || len(got.Titles) != 2 || got.Titles[1] != "tt2" || len(got.TVs) != 1 {
		t.Fatalf("get: %+v %v", got, err)
	}
	got.Name, got.Titles, got.TVs = "Children", []string{"tt3"}, nil
	if err := s.UpdateViewer(ctx, got); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateViewer(ctx, &Viewer{ID: got.ID, Name: "Rui"}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("rename onto another: %v", err)
	}
	list, err := s.ListViewers(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "Children" || len(list[0].Titles) != 1 || len(list[0].TVs) != 0 || len(list[1].Titles) != 0 {
		t.Fatalf("list: %+v %v", list, err)
	}

	// Progress is per viewer and goes with them.
	if _, err := s.SaveProgress(ctx, kids.ID, Progress{ItemID: "tt3", PositionSec: 60}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteViewer(ctx, kids.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetProgress(ctx, kids.ID, "tt3"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("progress kept: %v", err)
	}
	if err := s.DeleteViewer(ctx, admin.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting the admin as a viewer: %v", err)
	}
	// The freed slot is reused.
	z := &Viewer{Name: "Zé"}
	if err := s.CreateViewer(ctx, z); err != nil || z.Slot != 1 {
		t.Fatalf("reuse slot: %d %v", z.Slot, err)
	}
}

func TestTitleAllowed(t *testing.T) {
	titles := []string{"tt100", "tt200"}
	for id, want := range map[string]bool{"tt100": true, "tt200:1:3": true, "tt2000": false, "tt300:1:1": false, "": false} {
		if TitleAllowed(titles, id) != want {
			t.Errorf("%q: want %v", id, want)
		}
	}
}
