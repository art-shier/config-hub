package permissions

import "testing"

func TestProjectPermissionMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                string
		role, grant, action string
		want                bool
	}{
		{name: "admin manages projects", role: "admin", action: ActionManageProject, want: true},
		{name: "admin manages environments", role: "admin", action: ActionManageEnvironment, want: true},
		{name: "admin manages members", role: "admin", action: ActionManageMembers, want: true},
		{name: "admin reads configuration", role: "admin", action: ActionReadConfig, want: true},
		{name: "admin writes configuration", role: "admin", action: ActionWriteConfig, want: true},
		{name: "viewer reads", role: "member", grant: "viewer", action: ActionReadConfig, want: true},
		{name: "viewer does not write", role: "member", grant: "viewer", action: ActionWriteConfig, want: false},
		{name: "editor reads", role: "member", grant: "editor", action: ActionReadConfig, want: true},
		{name: "editor writes", role: "member", grant: "editor", action: ActionWriteConfig, want: true},
		{name: "editor cannot create environments", role: "member", grant: "editor", action: ActionManageEnvironment, want: false},
		{name: "editor cannot manage members", role: "member", grant: "editor", action: ActionManageMembers, want: false},
		{name: "ungranted member cannot read", role: "member", action: ActionReadConfig, want: false},
		{name: "unknown grant rejected", role: "member", grant: "owner", action: ActionReadConfig, want: false},
		{name: "unknown role rejected", role: "superadmin", action: ActionReadConfig, want: false},
		{name: "unknown action rejected", role: "admin", action: "delete_server", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Allowed(tc.role, tc.grant, tc.action); got != tc.want {
				t.Fatalf("Allowed(%q, %q, %q)=%v want=%v", tc.role, tc.grant, tc.action, got, tc.want)
			}
		})
	}
}
