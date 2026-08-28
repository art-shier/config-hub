package permissions

const (
	ActionManageProject     = "manage_project"
	ActionManageEnvironment = "manage_environment"
	ActionManageMembers     = "manage_members"
	ActionReadConfig        = "read_config"
	ActionWriteConfig       = "write_config"
)

// Allowed applies the system-role and project-grant matrix. Account enabled
// state is deliberately checked by the calling service because it is not part
// of this function's inputs.
func Allowed(role, grant, action string) bool {
	switch action {
	case ActionManageProject, ActionManageEnvironment, ActionManageMembers, ActionReadConfig, ActionWriteConfig:
	default:
		return false
	}
	if role == "admin" {
		return true
	}
	if role != "member" {
		return false
	}
	switch grant {
	case "viewer":
		return action == ActionReadConfig
	case "editor":
		return action == ActionReadConfig || action == ActionWriteConfig
	default:
		return false
	}
}
