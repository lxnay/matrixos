package ostree

import (
	"errors"
	"testing"

	"matrixos/vector/lib/config"
)

func TestBranchHelpers(t *testing.T) {
	tests := []struct {
		name     string
		ref      string
		remote   string
		cleanRef string
		isShort  bool
	}{
		{"Full Ref", "origin:matrixos/dev", "origin", "matrixos/dev", false},
		{"Short Ref", "gnome", "", "gnome", true},
		{"Ref No Remote", "matrixos/dev", "", "matrixos/dev", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractRemoteFromRef(tt.ref); got != tt.remote {
				t.Errorf("ExtractRemoteFromRef(%q) = %q, want %q", tt.ref, got, tt.remote)
			}
			if got := CleanRemoteFromRef(tt.ref); got != tt.cleanRef {
				t.Errorf("CleanRemoteFromRef(%q) = %q, want %q", tt.ref, got, tt.cleanRef)
			}
			if got := IsBranchShortName(tt.cleanRef); got != tt.isShort {
				t.Errorf("IsBranchShortName(%q) = %v, want %v", tt.cleanRef, got, tt.isShort)
			}
		})
	}
}

func TestBranchShortnameToNormal(t *testing.T) {
	got, err := BranchShortnameToNormal("dev", "gnome", "matrixos", "amd64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "matrixos/amd64/dev/gnome"
	if got != want {
		t.Errorf("BranchShortnameToNormal = %q, want %q", got, want)
	}

	gotProd, err := BranchShortnameToNormal("prod", "gnome", "matrixos", "amd64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantProd := "matrixos/amd64/gnome"
	if gotProd != wantProd {
		t.Errorf("BranchShortnameToNormal(prod) = %q, want %q", gotProd, wantProd)
	}
}

func TestBranchContainsRemote(t *testing.T) {
	tests := []struct {
		branch string
		want   bool
	}{
		{"origin:branch", true},
		{"branch", false},
		{"remote:group/branch", true},
	}
	for _, tt := range tests {
		if got := BranchContainsRemote(tt.branch); got != tt.want {
			t.Errorf("BranchContainsRemote(%q) = %v, want %v", tt.branch, got, tt.want)
		}
	}
}

func TestFullBranchHelpers(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.FullBranchSuffix": {"full"},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	// IsBranchFullSuffixed
	o.SetRef("branch-full")
	if isFull, _ := o.IsBranchFullSuffixed(); !isFull {
		t.Error("IsBranchFullSuffixed(branch-full) = false, want true")
	}
	o.SetRef("branch")
	if isFull, _ := o.IsBranchFullSuffixed(); isFull {
		t.Error("IsBranchFullSuffixed(branch) = true, want false")
	}

	// BranchToFull
	o.SetRef("branch")
	if full, _ := o.BranchToFull(); full != "branch-full" {
		t.Errorf("BranchToFull(branch) = %q, want branch-full", full)
	}
	o.SetRef("branch-full")
	if full, _ := o.BranchToFull(); full != "branch-full" {
		t.Errorf("BranchToFull(branch-full) = %q, want branch-full", full)
	}

	// RemoveFullFromBranch
	o.SetRef("branch-full")
	if clean, _ := o.RemoveFullFromBranch(); clean != "branch" {
		t.Errorf("RemoveFullFromBranch(branch-full) = %q, want branch", clean)
	}
	o.SetRef("branch")
	if clean, _ := o.RemoveFullFromBranch(); clean != "branch" {
		t.Errorf("RemoveFullFromBranch(branch) = %q, want branch", clean)
	}
}

func TestBranchHelpersErrors(t *testing.T) {
	if _, err := BranchShortnameToNormal("", "short", "os", "arch"); err == nil {
		t.Error("Should fail empty stage")
	}
	if _, err := BranchShortnameToNormal("stage", "", "os", "arch"); err == nil {
		t.Error("Should fail empty shortname")
	}
	if _, err := BranchShortnameToNormal("stage", "short", "", "arch"); err == nil {
		t.Error("Should fail empty os")
	}
	if _, err := BranchShortnameToNormal("stage", "short", "os", ""); err == nil {
		t.Error("Should fail empty arch")
	}
}

func TestOstreeBranchMethodsErrors(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.FullBranchSuffix": {"full"},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	if _, err := o.IsBranchFullSuffixed(); err == nil {
		t.Error("IsBranchFullSuffixed should fail empty ref")
	}
	if _, err := o.BranchToFull(); err == nil {
		t.Error("BranchToFull should fail empty ref")
	}
	if _, err := o.RemoveFullFromBranch(); err == nil {
		t.Error("RemoveFullFromBranch should fail empty ref")
	}
}

func TestCleanAndStripRef(t *testing.T) {
	if _, err := CleanAndStripRef(nil); err == nil {
		t.Error("expected error for nil IOstree")
	}
	got, err := CleanAndStripRef(&MockOstree{Ref_: "origin:matrixos/amd64/gnome-full"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "matrixos/amd64/gnome" {
		t.Errorf("got %q, want %q", got, "matrixos/amd64/gnome")
	}
	if _, err := CleanAndStripRef(&MockOstree{RemoveFullErr: errors.New("boom")}); err == nil {
		t.Error("expected propagated RemoveFullFromBranch error")
	}
	if _, err := CleanAndStripRef(&MockOstree{RemoveFullResult: "origin:", RemoveFullResultSet: true}); err == nil {
		t.Error("expected error when cleaned ref is empty")
	}
}
