package projects

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"confighub.local/internal/auth"
	"confighub.local/internal/database"
)

func TestListVisibleOnlyReturnsGrantedProjectsInSlugOrder(t *testing.T) {
	service, actors := projectFixture(t)
	ctx := context.Background()
	for _, input := range []CreateProject{
		{Slug: "z-hidden", Name: "Hidden"},
		{Slug: "b-editor", Name: "Editor"},
		{Slug: "a-viewer", Name: "Viewer"},
	} {
		if _, err := service.CreateProject(ctx, actors.admin, input); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.SetMember(ctx, actors.admin, "a-viewer", actors.member.Username, "viewer"); err != nil {
		t.Fatal(err)
	}
	if err := service.SetMember(ctx, actors.admin, "b-editor", actors.member.Username, "editor"); err != nil {
		t.Fatal(err)
	}

	visible, err := service.ListVisible(ctx, actors.member)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 2 || visible[0].Slug != "a-viewer" || visible[1].Slug != "b-editor" {
		t.Fatalf("visible=%+v", visible)
	}
	all, err := service.ListVisible(ctx, actors.admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Slug != "a-viewer" || all[2].Slug != "z-hidden" {
		t.Fatalf("all=%+v", all)
	}
}

func TestDisabledActorsHaveNoProjectAccess(t *testing.T) {
	service, actors := projectFixture(t)
	ctx := context.Background()
	if _, err := service.CreateProject(ctx, actors.admin, CreateProject{Slug: "app", Name: "App"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListVisible(ctx, actors.disabled); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ListVisible error=%v", err)
	}
	if _, err := service.GetProject(ctx, actors.disabled, "app"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("GetProject error=%v", err)
	}
	if _, err := service.CreateProject(ctx, actors.disabled, CreateProject{Slug: "no", Name: "No"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateProject error=%v", err)
	}
}

func TestAdminCreatesProjectsAndEnvironmentsButEditorCannot(t *testing.T) {
	service, actors := projectFixture(t)
	ctx := context.Background()
	project, err := service.CreateProject(ctx, actors.admin, CreateProject{Slug: " app-one ", Name: " Application ", Description: " primary "})
	if err == nil {
		t.Fatal("whitespace-padded slug accepted")
	}
	project, err = service.CreateProject(ctx, actors.admin, CreateProject{Slug: "app-one", Name: " Application ", Description: " primary "})
	if err != nil {
		t.Fatal(err)
	}
	if project.Slug != "app-one" || project.Name != "Application" || project.Description != "primary" || project.ID == "" {
		t.Fatalf("project=%+v", project)
	}
	if project.CreatedAt.Location() != time.UTC || !project.CreatedAt.Equal(project.CreatedAt.Truncate(time.Second)) || !project.UpdatedAt.Equal(project.CreatedAt) {
		t.Fatalf("timestamps=%v/%v", project.CreatedAt, project.UpdatedAt)
	}
	if err := service.SetMember(ctx, actors.admin, project.Slug, actors.member.Username, "editor"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateEnvironment(ctx, actors.member, project.Slug, CreateEnvironment{Slug: "production", Name: "Production"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("editor create environment error=%v", err)
	}
	environment, err := service.CreateEnvironment(ctx, actors.admin, project.Slug, CreateEnvironment{Slug: "production", Name: " Production "})
	if err != nil {
		t.Fatal(err)
	}
	if environment.ProjectID != project.ID || environment.Name != "Production" || environment.Slug != "production" || environment.CurrentRevisionID != nil {
		t.Fatalf("environment=%+v", environment)
	}
}

func TestCreateValidationAndUniqueConflictsAreTyped(t *testing.T) {
	service, actors := projectFixture(t)
	ctx := context.Background()
	for _, test := range []struct {
		name  string
		input CreateProject
		field string
	}{
		{name: "uppercase slug", input: CreateProject{Slug: "Bad", Name: "Name"}, field: "slug"},
		{name: "empty name", input: CreateProject{Slug: "valid", Name: "  "}, field: "name"},
		{name: "long name", input: CreateProject{Slug: "valid", Name: string(make([]byte, MaxNameBytes+1))}, field: "name"},
		{name: "long description", input: CreateProject{Slug: "valid", Name: "Name", Description: string(make([]byte, MaxDescriptionBytes+1))}, field: "description"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.CreateProject(ctx, actors.admin, test.input)
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Fields[test.field] == "" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, err := service.CreateProject(ctx, actors.admin, CreateProject{Slug: "unique", Name: "First"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateProject(ctx, actors.admin, CreateProject{Slug: "unique", Name: "Second"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate project error=%v", err)
	}
	if _, err := service.CreateEnvironment(ctx, actors.admin, "unique", CreateEnvironment{Slug: "prod", Name: "Prod"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateEnvironment(ctx, actors.admin, "unique", CreateEnvironment{Slug: "prod", Name: "Other"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate environment error=%v", err)
	}
}

func TestProjectDetailAndMembersUseSafeAuthorizationAndStableOrdering(t *testing.T) {
	service, actors := projectFixture(t)
	ctx := context.Background()
	if _, err := service.CreateProject(ctx, actors.admin, CreateProject{Slug: "app", Name: "App"}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []CreateEnvironment{{Slug: "z-last", Name: "Last"}, {Slug: "a-first", Name: "First"}} {
		if _, err := service.CreateEnvironment(ctx, actors.admin, "app", input); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.GetProject(ctx, actors.member, "app"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ungranted existing error=%v", err)
	}
	if _, err := service.GetProject(ctx, actors.member, "missing"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ungranted missing error=%v", err)
	}
	if _, err := service.GetProject(ctx, actors.admin, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("admin missing error=%v", err)
	}
	if err := service.SetMember(ctx, actors.admin, "app", actors.member.Username, "viewer"); err != nil {
		t.Fatal(err)
	}
	detail, err := service.GetProject(ctx, actors.member, "app")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Permission != "viewer" || len(detail.Environments) != 2 || detail.Environments[0].Slug != "a-first" || detail.Environments[1].Slug != "z-last" {
		t.Fatalf("detail=%+v", detail)
	}
	members, err := service.ListMembers(ctx, actors.member, "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].Username != actors.member.Username || members[0].Permission != "viewer" {
		t.Fatalf("members=%+v", members)
	}
}

func TestMemberGrantSetUpdateAndRemovalSemantics(t *testing.T) {
	service, actors := projectFixture(t)
	ctx := context.Background()
	if _, err := service.CreateProject(ctx, actors.admin, CreateProject{Slug: "app", Name: "App"}); err != nil {
		t.Fatal(err)
	}
	for _, permission := range []string{"viewer", "viewer", "editor", "editor"} {
		if err := service.SetMember(ctx, actors.admin, "app", actors.member.Username, permission); err != nil {
			t.Fatalf("permission=%q error=%v", permission, err)
		}
	}
	members, err := service.ListMembers(ctx, actors.admin, "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].Permission != "editor" {
		t.Fatalf("members=%+v", members)
	}
	if err := service.SetMember(ctx, actors.member, "app", actors.member.Username, "viewer"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("editor managed members: %v", err)
	}
	if err := service.SetMember(ctx, actors.admin, "app", actors.admin.Username, "viewer"); !validationField(err, "username") {
		t.Fatalf("admin target error=%v", err)
	}
	if err := service.SetMember(ctx, actors.admin, "app", actors.disabled.Username, "viewer"); !validationField(err, "username") {
		t.Fatalf("disabled target error=%v", err)
	}
	if err := service.SetMember(ctx, actors.admin, "app", "missing", "viewer"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing target error=%v", err)
	}
	if err := service.SetMember(ctx, actors.admin, "app", actors.member.Username, "owner"); !validationField(err, "permission") {
		t.Fatalf("invalid permission error=%v", err)
	}
	for range 2 {
		if err := service.RemoveMember(ctx, actors.admin, "app", actors.member.Username); err != nil {
			t.Fatalf("idempotent removal error=%v", err)
		}
	}
	if err := service.RemoveMember(ctx, actors.member, "app", actors.member.Username); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member removed grant: %v", err)
	}
	if err := service.RemoveMember(ctx, actors.admin, "missing", actors.member.Username); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project removal error=%v", err)
	}
}

func validationField(err error, field string) bool {
	var validation *ValidationError
	return errors.As(err, &validation) && validation.Fields[field] != ""
}

type fixtureActors struct {
	admin, member, disabled auth.User
}

func projectFixture(t *testing.T) (*Service, fixtureActors) {
	t.Helper()
	store, err := database.Open(filepath.Join(t.TempDir(), "projects.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	actors := fixtureActors{
		admin:    auth.User{ID: "admin-id", Username: "admin", DisplayName: "Admin", Role: "admin", Enabled: true},
		member:   auth.User{ID: "member-id", Username: "member", DisplayName: "Member", Role: "member", Enabled: true},
		disabled: auth.User{ID: "disabled-id", Username: "disabled", DisplayName: "Disabled", Role: "member", Enabled: false},
	}
	for _, actor := range []auth.User{actors.admin, actors.member, actors.disabled} {
		enabled := 0
		if actor.Enabled {
			enabled = 1
		}
		if _, err := store.DB().Exec(`INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES (?, ?, ?, 'hash', ?, ?, 1, 1)`, actor.ID, actor.Username, actor.DisplayName, actor.Role, enabled); err != nil {
			t.Fatal(err)
		}
	}
	return NewService(store), actors
}
