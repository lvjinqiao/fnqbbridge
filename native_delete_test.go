package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNativeMapping(t *testing.T) {
	h := strings.Repeat("a", 40)
	rows := []nativeRow{{ID: 3, UID: 1000, Type: "qbt", Hash: h}, {ID: 4, UID: 1001, Type: "qbt", Hash: h}}
	ids, hashes, e := planNative(rows, 1000, strings.ToUpper(h)+"|"+h)
	if e != nil || len(ids) != 1 || ids[0] != 3 || len(hashes) != 1 {
		t.Fatal(ids, hashes, e)
	}
	for _, v := range []string{"", "all", h + "|", strings.Repeat("z", 40)} {
		if _, _, e = planNative(rows, 1000, v); e == nil {
			t.Fatal("unsafe input")
		}
	}
	rows = append(rows, nativeRow{ID: 5, UID: 1000, Type: "qbt", Hash: h})
	if _, _, e = planNative(rows, 1000, h); e == nil {
		t.Fatal("ambiguous mapping")
	}
}
func TestNativeDeletionBothModes(t *testing.T) {
	for _, files := range []bool{false, true} {
		t.Run(map[bool]string{false: "keep", true: "files"}[files], func(t *testing.T) {
			h := strings.Repeat("b", 40)
			rows := []nativeRow{{ID: 7, UID: 1000, Type: "qbt", Hash: h}}
			calls := 0
			a := &App{}
			a.native = &nativeOps{Owner: func() (int, error) { return 1000, nil }, Rows: func(context.Context, int) ([]nativeRow, error) { return rows, nil }, Delete: func(_ context.Context, uid int, ids []int64, del bool) error {
				calls++
				if uid != 1000 || len(ids) != 1 || ids[0] != 7 || del != files {
					t.Fatal("wrong delete")
				}
				rows = nil
				return nil
			}, EngineHas: func(context.Context, string) (bool, error) { return false, nil }}
			if e := a.deleteNative(context.Background(), h, files); e != nil {
				t.Fatal(e)
			}
			if calls != 1 {
				t.Fatal("mutation retried")
			}
			if e := a.deleteNative(context.Background(), h, files); e == nil {
				t.Fatal("unknown duplicate delete accepted")
			}
			if calls != 1 {
				t.Fatal("duplicate invoked native")
			}
		})
	}
}
func TestNativeFailureDoesNotInvokeQBDelete(t *testing.T) {
	h := strings.Repeat("c", 40)
	rows := []nativeRow{{ID: 9, UID: 1000, Type: "qbt", Hash: h}}
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{}
	calls := 0
	a.native = &nativeOps{Owner: func() (int, error) { return 1000, nil }, Rows: func(context.Context, int) ([]nativeRow, error) { return rows, nil }, Delete: func(context.Context, int, []int64, bool) error {
		calls++
		cancel()
		return errors.New("native timeout")
	}, EngineHas: func(context.Context, string) (bool, error) { return true, nil }}
	if e := a.deleteNative(ctx, h, false); e == nil {
		t.Fatal("failure hidden")
	}
	if calls != 1 {
		t.Fatal("delete retried")
	}
}
func TestCrossUIDNoMutation(t *testing.T) {
	h := strings.Repeat("d", 40)
	a := &App{}
	a.native = &nativeOps{Owner: func() (int, error) { return 1000, nil }, Rows: func(context.Context, int) ([]nativeRow, error) {
		return []nativeRow{{ID: 1, UID: 1001, Type: "qbt", Hash: h}}, nil
	}, Delete: func(context.Context, int, []int64, bool) error { t.Fatal("cross UID delete"); return nil }}
	if e := a.deleteNative(context.Background(), h, false); e == nil {
		t.Fatal("cross UID mapping accepted")
	}
}
func TestZombieNativeRecordCanBePlanned(t *testing.T) {
	h := strings.Repeat("e", 40)
	rows := []nativeRow{{ID: 11, UID: 1000, Type: "qbt", Hash: h}}
	a := &App{}
	calls := 0
	a.native = &nativeOps{Owner: func() (int, error) { return 1000, nil }, Rows: func(context.Context, int) ([]nativeRow, error) { return rows, nil }, Delete: func(context.Context, int, []int64, bool) error { calls++; rows = nil; return nil }, EngineHas: func(context.Context, string) (bool, error) { return false, nil }}
	if e := a.deleteNative(context.Background(), h, false); e != nil || calls != 1 {
		t.Fatal("zombie record skipped", e)
	}
}

func TestNativeDeletionWaitsForAsynchronousCleanup(t *testing.T) {
	h := strings.Repeat("f", 40)
	row := nativeRow{ID: 12, UID: 1000, Type: "qbt", Hash: h}
	reads, calls := 0, 0
	a := &App{}
	a.native = &nativeOps{Owner: func() (int, error) { return 1000, nil }, Rows: func(context.Context, int) ([]nativeRow, error) {
		reads++
		if reads <= 8 {
			return []nativeRow{row}, nil
		}
		return nil, nil
	}, Delete: func(context.Context, int, []int64, bool) error { calls++; return nil }, EngineHas: func(context.Context, string) (bool, error) { return false, nil }}
	if err := a.deleteNative(context.Background(), h, true); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("asynchronous cleanup retried delete")
	}
}
