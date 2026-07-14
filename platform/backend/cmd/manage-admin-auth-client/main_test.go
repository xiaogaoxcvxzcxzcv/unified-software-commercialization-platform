package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"platform.local/capability-platform/backend/internal/modules/identity"
	"platform.local/capability-platform/backend/internal/platform/config"
)

type serviceStub struct {
	registerCommand identity.RegisterControlledClientCommand
	rotateCommand   identity.RotateControlledClientCredentialCommand
	disabledClient  string
	revokedClient   string
	revokedCred     string
	issued          identity.IssuedControlledClientCredential
	err             error
}

func (s *serviceStub) RegisterControlledAdminClient(_ context.Context, command identity.RegisterControlledClientCommand) (identity.IssuedControlledClientCredential, error) {
	s.registerCommand = command
	return s.issued, s.err
}

func (s *serviceStub) RotateControlledAdminClientCredential(_ context.Context, command identity.RotateControlledClientCredentialCommand) (identity.IssuedControlledClientCredential, error) {
	s.rotateCommand = command
	return s.issued, s.err
}

func (s *serviceStub) DisableControlledAdminClient(_ context.Context, clientID string) error {
	s.disabledClient = clientID
	return s.err
}

func (s *serviceStub) RevokeControlledAdminClientCredential(_ context.Context, clientID, credentialID string) error {
	s.revokedClient, s.revokedCred = clientID, credentialID
	return s.err
}

func TestRunCreateOutputsCredentialExactlyOnce(t *testing.T) {
	t.Parallel()
	expires := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	stub := &serviceStub{issued: identity.IssuedControlledClientCredential{
		ClientID: "acli_test", CredentialID: "acred_test", ProofType: "shared_secret_v1",
		Secret: "acsec_one-time-secret", ExpiresAt: &expires,
	}}
	var output bytes.Buffer
	closed := false
	err := run(context.Background(), []string{
		"create", "--display-name", " Release CLI ", "--client-type", "cli", "--expires-at", expires.Format(time.RFC3339),
	}, nil, &output, testDependencies(stub, &closed))
	if err != nil {
		t.Fatalf("run create: %v", err)
	}
	if stub.registerCommand.DisplayName != "Release CLI" || stub.registerCommand.ClientType != "cli" || stub.registerCommand.ExpiresAt == nil || !stub.registerCommand.ExpiresAt.Equal(expires) {
		t.Fatalf("register command = %#v", stub.registerCommand)
	}
	if count := strings.Count(output.String(), stub.issued.Secret); count != 1 {
		t.Fatalf("secret output count = %d, output = %q", count, output.String())
	}
	if !strings.Contains(output.String(), `"client_id":"acli_test"`) || !strings.HasSuffix(output.String(), "\n") {
		t.Fatalf("unexpected output: %q", output.String())
	}
	if !closed {
		t.Fatal("service handle was not closed")
	}
}

func TestRunRotateOutputsNewCredentialOnce(t *testing.T) {
	t.Parallel()
	stub := &serviceStub{issued: identity.IssuedControlledClientCredential{
		ClientID: "acli_test", CredentialID: "acred_rotated", ProofType: "shared_secret_v1", Secret: "acsec_rotated",
	}}
	var output bytes.Buffer
	err := run(context.Background(), []string{"rotate", "--client-id", " acli_test "}, nil, &output, testDependencies(stub, nil))
	if err != nil {
		t.Fatalf("run rotate: %v", err)
	}
	if stub.rotateCommand.ClientID != "acli_test" {
		t.Fatalf("rotate client = %q", stub.rotateCommand.ClientID)
	}
	if count := strings.Count(output.String(), stub.issued.Secret); count != 1 {
		t.Fatalf("secret output count = %d", count)
	}
}

func TestRunDisableAndRevokeNeverOutputSecret(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "disable", args: []string{"disable", "--client-id", "acli_test"}, want: `"action":"disable"`},
		{name: "revoke", args: []string{"revoke", "--client-id", "acli_test", "--credential-id", "acred_test"}, want: `"action":"revoke"`},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stub := &serviceStub{}
			var output bytes.Buffer
			if err := run(context.Background(), test.args, nil, &output, testDependencies(stub, nil)); err != nil {
				t.Fatalf("run: %v", err)
			}
			if !strings.Contains(output.String(), test.want) || strings.Contains(output.String(), "secret") {
				t.Fatalf("unexpected output: %q", output.String())
			}
		})
	}
}

func TestRunSanitizesServiceAndInitializationErrors(t *testing.T) {
	t.Parallel()
	const secret = "acsec_must-not-leak"
	stub := &serviceStub{err: errors.New("repository failed with " + secret)}
	var output bytes.Buffer
	err := run(context.Background(), []string{"rotate", "--client-id", "acli_test"}, nil, &output, testDependencies(stub, nil))
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(output.String(), secret) {
		t.Fatalf("service error leaked secret: err=%v output=%q", err, output.String())
	}

	deps := testDependencies(stub, nil)
	deps.openService = func(context.Context, config.Config) (serviceHandle, error) {
		return serviceHandle{}, errors.New("connect using postgres://user:" + secret + "@host/db")
	}
	err = run(context.Background(), []string{"disable", "--client-id", "acli_test"}, nil, &output, deps)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("initialization error leaked secret: %v", err)
	}
}

func TestRunRejectsInvalidCommandsBeforeLoadingConfiguration(t *testing.T) {
	t.Parallel()
	invalid := [][]string{
		nil,
		{"unknown"},
		{"create", "--display-name", "x", "--client-type", "browser"},
		{"create", "--display-name", "x", "--client-type", "cli", "--expires-at", "tomorrow"},
		{"rotate"},
		{"disable", "--client-id", ""},
		{"revoke", "--client-id", "acli_test"},
		{"disable", "--client-id", "acli_test", "extra"},
	}
	for _, args := range invalid {
		args := args
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			t.Parallel()
			loaded := false
			deps := dependencies{loadConfig: func(config.LookupEnv) (config.Config, error) {
				loaded = true
				return config.Config{}, nil
			}}
			err := run(context.Background(), args, nil, &bytes.Buffer{}, deps)
			if !errors.Is(err, errUsage) {
				t.Fatalf("error = %v, want usage error", err)
			}
			if loaded {
				t.Fatal("configuration loaded for invalid command")
			}
		})
	}
}

func TestRunHonorsCanceledContextBeforeInitialization(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	opened := false
	deps := testDependencies(&serviceStub{}, nil)
	deps.openService = func(context.Context, config.Config) (serviceHandle, error) {
		opened = true
		return serviceHandle{}, nil
	}
	err := run(ctx, []string{"disable", "--client-id", "acli_test"}, nil, &bytes.Buffer{}, deps)
	if err == nil || opened {
		t.Fatalf("canceled run: err=%v opened=%v", err, opened)
	}
}

func TestWriteJSONRejectsShortWriteWithoutIncludingPayloadInError(t *testing.T) {
	t.Parallel()
	const secret = "acsec_short-write"
	err := writeJSON(shortWriter{}, credentialOutput{Secret: secret})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("write error = %v", err)
	}
}

type shortWriter struct{}

func (shortWriter) Write(payload []byte) (int, error) { return len(payload) - 1, nil }

func testDependencies(service adminClientService, closed *bool) dependencies {
	return dependencies{
		loadConfig: func(config.LookupEnv) (config.Config, error) { return config.Config{}, nil },
		openService: func(context.Context, config.Config) (serviceHandle, error) {
			return serviceHandle{service: service, close: func() {
				if closed != nil {
					*closed = true
				}
			}}, nil
		},
	}
}
