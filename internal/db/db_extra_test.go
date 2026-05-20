package db

import (
	"testing"
	"time"
)

func TestUserCRUD(t *testing.T) {
	db := setupTestDB(t)

	// Test EnsureDefaultAdmin
	err := db.EnsureDefaultAdmin("admin-password-hash")
	if err != nil {
		t.Fatalf("EnsureDefaultAdmin failed: %v", err)
	}

	admin, err := db.GetUser("admin")
	if err != nil {
		t.Fatalf("GetUser admin failed: %v", err)
	}
	if admin.Role != "admin" {
		t.Errorf("expected role admin, got %s", admin.Role)
	}

	// Test CreateUser & GetUser
	user := User{
		ID:           "user-1",
		Username:     "john",
		PasswordHash: "hash123",
		Role:         "viewer",
		CreatedAt:    time.Now().Unix(),
	}
	err = db.CreateUser(user)
	if err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}

	got, err := db.GetUser("john")
	if err != nil {
		t.Fatalf("GetUser failed: %v", err)
	}
	if got.ID != user.ID || got.Role != "viewer" {
		t.Errorf("GetUser returned incorrect user: %+v", got)
	}

	// Test UpdatePassword
	err = db.UpdatePassword("john", "newhash")
	if err != nil {
		t.Fatalf("UpdatePassword failed: %v", err)
	}
	got, _ = db.GetUser("john")
	if got.PasswordHash != "newhash" {
		t.Errorf("expected password hash newhash, got %s", got.PasswordHash)
	}

	// Test UpdatePasswordWithVersion
	err = db.UpdatePasswordWithVersion("john", "newerhash")
	if err != nil {
		t.Fatalf("UpdatePasswordWithVersion failed: %v", err)
	}
	got, _ = db.GetUser("john")
	if got.PasswordHash != "newerhash" || got.TokenVersion != 1 {
		t.Errorf("expected version to be incremented, got %+v", got)
	}

	// Test IncrementTokenVersion
	err = db.IncrementTokenVersion("john")
	if err != nil {
		t.Fatalf("IncrementTokenVersion failed: %v", err)
	}
	got, _ = db.GetUser("john")
	if got.TokenVersion != 2 {
		t.Errorf("expected token version to be 2, got %d", got.TokenVersion)
	}
}

func TestServiceCRUD(t *testing.T) {
	db := setupTestDB(t)

	svc := Service{
		ID:         "svc-1",
		InstanceID: "inst-1",
		Name:       "web-app",
		Type:       "compose",
		CreatedAt:  time.Now().Unix(),
	}

	err := db.SaveService(svc)
	if err != nil {
		t.Fatalf("SaveService failed: %v", err)
	}

	got, err := db.GetService("svc-1")
	if err != nil {
		t.Fatalf("GetService failed: %v", err)
	}
	if got.Name != "web-app" {
		t.Errorf("expected web-app, got %s", got.Name)
	}

	svcs, err := db.ListServices()
	if err != nil {
		t.Fatalf("ListServices failed: %v", err)
	}
	if len(svcs) != 1 {
		t.Errorf("expected 1 service, got %d", len(svcs))
	}

	scoped, err := db.ListServicesByInstance("inst-1")
	if err != nil {
		t.Fatalf("ListServicesByInstance failed: %v", err)
	}
	if len(scoped) != 1 {
		t.Errorf("expected 1 scoped service, got %d", len(scoped))
	}

	err = db.DeleteService("svc-1")
	if err != nil {
		t.Fatalf("DeleteService failed: %v", err)
	}

	_, err = db.GetService("svc-1")
	if err == nil {
		t.Error("expected error getting deleted service, got nil")
	}
}

func TestDomainCRUD(t *testing.T) {
	db := setupTestDB(t)

	dom := Domain{
		ID:         "dom-1",
		InstanceID: "inst-1",
		Domain:     "example.com",
		Service:    "web-app",
		Port:       80,
	}

	err := db.SaveDomain(dom)
	if err != nil {
		t.Fatalf("SaveDomain failed: %v", err)
	}

	doms, err := db.ListDomains()
	if err != nil {
		t.Fatalf("ListDomains failed: %v", err)
	}
	if len(doms) != 1 {
		t.Errorf("expected 1 domain, got %d", len(doms))
	}

	scoped, err := db.ListDomainsByInstance("inst-1")
	if err != nil {
		t.Fatalf("ListDomainsByInstance failed: %v", err)
	}
	if len(scoped) != 1 {
		t.Errorf("expected 1 scoped domain, got %d", len(scoped))
	}

	err = db.DeleteDomain("dom-1")
	if err != nil {
		t.Fatalf("DeleteDomain failed: %v", err)
	}
}

func TestInstanceExtraCRUD(t *testing.T) {
	db := setupTestDB(t)

	// Test EnsureLocalInstance
	err := db.EnsureLocalInstance()
	if err != nil {
		t.Fatalf("EnsureLocalInstance failed: %v", err)
	}

	local, err := db.GetInstance("local")
	if err != nil {
		t.Fatalf("GetInstance local failed: %v", err)
	}
	if local.Mode != "local" {
		t.Errorf("expected mode local, got %s", local.Mode)
	}

	// Test UpdateInstanceInfo
	info := Instance{
		DockerVersion: "24.0.7",
		OS:            "Ubuntu 22.04 LTS",
		CPUCores:      4,
		TotalMemory:   8192,
	}
	err = db.UpdateInstanceInfo("local", info)
	if err != nil {
		t.Fatalf("UpdateInstanceInfo failed: %v", err)
	}

	updated, err := db.GetInstance("local")
	if err != nil {
		t.Fatalf("GetInstance local failed: %v", err)
	}
	if updated.DockerVersion != "24.0.7" || updated.OS != "Ubuntu 22.04 LTS" {
		t.Errorf("UpdateInstanceInfo did not apply correctly: %+v", updated)
	}

	// Test UpdateInstanceStatus & LastSeen
	err = db.UpdateInstanceStatus("local", "offline")
	if err != nil {
		t.Fatalf("UpdateInstanceStatus failed: %v", err)
	}
	updated, _ = db.GetInstance("local")
	if updated.Status != "offline" {
		t.Errorf("expected status offline, got %s", updated.Status)
	}

	err = db.UpdateInstanceLastSeen("local", 123456789)
	if err != nil {
		t.Fatalf("UpdateInstanceLastSeen failed: %v", err)
	}
	updated, _ = db.GetInstance("local")
	if updated.LastSeen != 123456789 {
		t.Errorf("expected last seen 123456789, got %d", updated.LastSeen)
	}

	instances, err := db.ListInstances()
	if err != nil {
		t.Fatalf("ListInstances failed: %v", err)
	}
	if len(instances) != 1 {
		t.Errorf("expected 1 instance, got %d", len(instances))
	}
}
