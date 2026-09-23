package auth

import "testing"

func TestAdminSecurityMutationsRejectAdministratorTargets(t *testing.T) {
	if err := allowAdminSecurityMutation(User{Role: RoleAdmin}); err != ErrNotPermitted {
		t.Fatalf("administrator target = %v, want %v", err, ErrNotPermitted)
	}
	if err := allowAdminSecurityMutation(User{Role: RoleUser}); err != nil {
		t.Fatalf("ordinary user target = %v, want nil", err)
	}
}
