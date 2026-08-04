package core_test

import (
	"testing"

	uc "github.com/aleksclark/ultracore"
)

func TestRunPolicyAllowsTool(t *testing.T) {
	p := uc.RunPolicy{AllowTools: []string{"bash", "view"}, DenyTools: []string{"bash"}}
	if p.AllowsTool("bash") {
		t.Fatal("deny should win")
	}
	if !p.AllowsTool("view") {
		t.Fatal("view should be allowed")
	}
	if p.AllowsTool("write") {
		t.Fatal("write not on allow list")
	}
	star := uc.RunPolicy{AllowTools: []string{"*"}}
	if !star.AllowsTool("anything") {
		t.Fatal("star allows all")
	}
	starDeny := uc.RunPolicy{AllowTools: []string{"*"}, DenyTools: []string{"write"}}
	if starDeny.AllowsTool("write") {
		t.Fatal("star still respects deny")
	}
}

func TestRunPolicyResourceKinds(t *testing.T) {
	none := uc.RunPolicy{}
	if none.AllowsResourceKind(uc.ResourceKindDevEnv) {
		t.Fatal("empty kinds means none")
	}
	all := uc.RunPolicy{ResourceKinds: []uc.ResourceKind{"*"}}
	if !all.AllowsResourceKind(uc.ResourceKindDevEnv) {
		t.Fatal("star allows all kinds")
	}
	one := uc.RunPolicy{ResourceKinds: []uc.ResourceKind{uc.ResourceKindDevEnv}}
	if !one.AllowsResourceKind(uc.ResourceKindDevEnv) {
		t.Fatal("explicit kind")
	}
	if one.AllowsResourceKind("other") {
		t.Fatal("other kind denied")
	}
}

func TestRunPolicySubsetOf(t *testing.T) {
	parent := uc.RunPolicy{
		AllowTools: []string{"bash", "view", "spawn_agent"}, DenyTools: []string{"bash"},
		ResourceKinds: []uc.ResourceKind{uc.ResourceKindDevEnv}, MaxChildren: 4,
	}
	// Child narrower tools.
	child := uc.RunPolicy{AllowTools: []string{"view"}, ResourceKinds: []uc.ResourceKind{uc.ResourceKindDevEnv}, MaxChildren: 2}
	if !child.IsSubset(parent) {
		t.Fatal("narrower child should be subset")
	}
	// Child tries denied tool.
	bad := uc.RunPolicy{AllowTools: []string{"bash"}, MaxChildren: 1}
	if bad.IsSubset(parent) {
		t.Fatal("child allowing denied tool is not subset")
	}
	// Child tries more children.
	tooMany := uc.RunPolicy{AllowTools: []string{"view"}, MaxChildren: 5}
	if tooMany.IsSubset(parent) {
		t.Fatal("MaxChildren must not exceed parent")
	}
	// Child tries extra kind.
	kind := uc.RunPolicy{AllowTools: []string{"view"}, ResourceKinds: []uc.ResourceKind{"*"}, MaxChildren: 1}
	if kind.IsSubset(parent) {
		t.Fatal("kinds must be subset")
	}
	// Star parent, finite child.
	starParent := uc.RunPolicy{AllowTools: []string{"*"}, MaxChildren: 8, ResourceKinds: []uc.ResourceKind{"*"}}
	finite := uc.RunPolicy{AllowTools: []string{"bash"}, MaxChildren: 3, ResourceKinds: []uc.ResourceKind{uc.ResourceKindDevEnv}}
	if !finite.IsSubset(starParent) {
		t.Fatal("finite under star parent")
	}
}
