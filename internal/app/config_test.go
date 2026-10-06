package app

import (
	"strings"
	"testing"
)

func mapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func requiredEnv() map[string]string {
	return map[string]string{
		envBaseURL:           "https://archfit.example",
		envAppID:             "12345",
		envPrivateKeyFile:    "/run/secrets/key.pem",
		envWebhookSecretFile: "/run/secrets/webhook",
		envOAuthClientID:     "Iv1.client",
		envOAuthSecretFile:   "/run/secrets/oauth",
		envCookieKeyFile:     "/run/secrets/cookie",
		envOperatorTokenFile: "/run/secrets/operator",
	}
}

func with(m map[string]string, k, v string) map[string]string {
	m[k] = v
	return m
}

func without(m map[string]string, k string) map[string]string {
	delete(m, k)
	return m
}

func TestLoadDefaultsAndRequired(t *testing.T) {
	requiredOnly := Config{
		Addr:              ":8080",
		BaseURL:           "https://archfit.example",
		AppID:             12345,
		PrivateKeyFile:    "/run/secrets/key.pem",
		WebhookSecretFile: "/run/secrets/webhook",
		OAuthClientID:     "Iv1.client",
		OAuthSecretFile:   "/run/secrets/oauth",
		CookieKeyFile:     "/run/secrets/cookie",
		OperatorTokenFile: "/run/secrets/operator",
		StoreDriver:       "memory",
		SandboxDriver:     "none",
		Role:              "all",
		QueueDriver:       "inline",
	}
	everything := requiredOnly
	everything.Addr = "127.0.0.1:9000"
	everything.AnthropicKeyFile = "/run/secrets/anthropic"
	everything.Role, everything.OperatorTokenFile = "public", "/run/secrets/operator"
	everything.QueueDriver = "cloudtasks"
	everything.TasksQueue = "projects/coupling-test/locations/europe-west1/queues/webhooks"
	everything.WorkerURL = "https://worker.example"
	everything.Invoker = "invoker@coupling-test.iam.gserviceaccount.com"
	public := requiredOnly
	public.Role, public.OperatorTokenFile = "public", ""
	worker := requiredOnly
	worker.Role, worker.WorkerURL, worker.Invoker = "worker", everything.WorkerURL, everything.Invoker
	workerEnv := func() map[string]string {
		return with(with(with(requiredEnv(), envRole, "worker"), envWorkerURL, worker.WorkerURL), envInvoker, worker.Invoker)
	}
	cloudEnv := func() map[string]string {
		m := with(with(with(requiredEnv(), envRole, "public"), envQueue, "cloudtasks"), envTasksQueue, everything.TasksQueue)
		return with(with(m, envWorkerURL, everything.WorkerURL), envInvoker, everything.Invoker)
	}
	underPath := requiredOnly
	underPath.BaseURL = "https://archfit.example/app"
	onFirestore := requiredOnly
	onFirestore.StoreDriver = "firestore"
	onFirestore.FirestoreProject = "coupling-dev-510710"
	onFirestore.FirestoreDatabase = "archfit"
	onEmulator := requiredOnly
	onEmulator.StoreDriver = "firestore"
	onEmulator.FirestoreProject = "archfit-local"
	onEmulator.FirestoreEmulator = "127.0.0.1:8086"
	firestoreEnv := func(project string) map[string]string {
		return with(with(requiredEnv(), envStore, "firestore"), envFirestoreProject, project)
	}

	all := requiredEnv()
	all[envAddr] = "127.0.0.1:9000"
	all[envAnthropicKeyFile] = "/run/secrets/anthropic"
	all[envStore] = "memory"
	all[envSandbox] = "none"
	all[envRole] = "public"
	all[envQueue] = "cloudtasks"
	all[envTasksQueue] = everything.TasksQueue
	all[envWorkerURL] = everything.WorkerURL
	all[envInvoker] = everything.Invoker

	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr string // the variable the error must name
	}{
		{name: "empty", env: map[string]string{}, wantErr: envBaseURL},
		{name: "only required", env: requiredEnv(), want: requiredOnly},
		{name: "every variable", env: all, want: everything},
		{name: "non-numeric app ID", env: with(requiredEnv(), envAppID, "MARKER-not-a-number"), wantErr: envAppID},
		{name: "missing app ID", env: without(requiredEnv(), envAppID), wantErr: envAppID},
		{name: "missing private key file", env: without(requiredEnv(), envPrivateKeyFile), wantErr: envPrivateKeyFile},
		{name: "missing webhook secret file", env: without(requiredEnv(), envWebhookSecretFile), wantErr: envWebhookSecretFile},
		{name: "missing OAuth client ID", env: without(requiredEnv(), envOAuthClientID), wantErr: envOAuthClientID},
		{name: "missing OAuth secret file", env: without(requiredEnv(), envOAuthSecretFile), wantErr: envOAuthSecretFile},
		{name: "missing cookie key file", env: without(requiredEnv(), envCookieKeyFile), wantErr: envCookieKeyFile},
		{name: "missing operator token file", env: without(requiredEnv(), envOperatorTokenFile), wantErr: envOperatorTokenFile},
		{name: "first missing wins", env: without(without(requiredEnv(), envOperatorTokenFile), envAppID), wantErr: envAppID},
		{name: "unknown store", env: with(requiredEnv(), envStore, "MARKER-redis"), wantErr: envStore},
		{name: "unknown sandbox", env: with(requiredEnv(), envSandbox, "MARKER-docker"), wantErr: envSandbox},
		{name: "firestore without a project", env: firestoreEnv(""), wantErr: envFirestoreProject},
		{name: "firestore with a named database", env: with(firestoreEnv("coupling-dev-510710"), envFirestoreDatabase, "archfit"), want: onFirestore},
		{name: "firestore emulator", env: with(firestoreEnv("archfit-local"), envFirestoreEmulator, "127.0.0.1:8086"), want: onEmulator},
		// The base URL is the OIDC audience, compared byte for byte, and the Action refuses an endpoint ending in '/'.
		{name: "base URL with trailing slash", env: with(requiredEnv(), envBaseURL, "https://MARKER.example/"), wantErr: envBaseURL},
		{name: "base URL path with trailing slash", env: with(requiredEnv(), envBaseURL, "https://archfit.example/app/"), wantErr: envBaseURL},
		{name: "base URL slash only", env: with(requiredEnv(), envBaseURL, "/"), wantErr: envBaseURL},
		{name: "base URL with a path", env: with(requiredEnv(), envBaseURL, "https://archfit.example/app"), want: underPath},
		{name: "unknown role", env: with(requiredEnv(), envRole, "MARKER-batch"), wantErr: envRole},
		{name: "unknown queue", env: with(requiredEnv(), envQueue, "MARKER-pubsub"), wantErr: envQueue},
		{name: "cloudtasks in the all role", env: with(cloudEnv(), envRole, "all"), wantErr: envQueue},
		{name: "public role without the operator token", env: without(with(requiredEnv(), envRole, "public"), envOperatorTokenFile), want: public},
		{name: "worker role", env: workerEnv(), want: worker},
		{name: "worker role without the operator token", env: without(workerEnv(), envOperatorTokenFile), wantErr: envOperatorTokenFile},
		{name: "worker role without the worker URL", env: without(workerEnv(), envWorkerURL), wantErr: envWorkerURL},
		{name: "worker role without the invoker", env: without(workerEnv(), envInvoker), wantErr: envInvoker},
		{name: "worker role ignores the queue driver", env: with(workerEnv(), envQueue, "cloudtasks"), want: func() Config { c := worker; c.QueueDriver = "cloudtasks"; return c }()},
		{name: "cloudtasks without the queue name", env: without(cloudEnv(), envTasksQueue), wantErr: envTasksQueue},
		{name: "cloudtasks without the worker URL", env: without(cloudEnv(), envWorkerURL), wantErr: envWorkerURL},
		{name: "cloudtasks without the invoker", env: without(cloudEnv(), envInvoker), wantErr: envInvoker},
		{name: "invoker that is not an e-mail", env: with(cloudEnv(), envInvoker, "MARKER-invoker"), wantErr: envInvoker},
		{name: "worker URL with trailing slash", env: with(cloudEnv(), envWorkerURL, "https://MARKER.example/"), wantErr: envWorkerURL},
		{name: "worker URL over plain http", env: with(cloudEnv(), envWorkerURL, "http://MARKER.example"), wantErr: envWorkerURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(mapEnv(tt.env))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				if got != tt.want {
					t.Errorf("Load = %+v, want %+v", got, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("Load = %+v, want an error naming %s", got, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr+" ") {
				t.Errorf("error %q does not name %s", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "MARKER") {
				t.Errorf("error %q echoes a value", err)
			}
		})
	}
}

func TestLoadNeverReadsSecretValuesFromEnv(t *testing.T) {
	const inlinePEM = "-----BEGIN RSA PRIVATE KEY-----\nMARKER\n-----END RSA PRIVATE KEY-----"
	inline := map[string]string{
		"ARCHFIT_GITHUB_PRIVATE_KEY":    inlinePEM,
		"ARCHFIT_GITHUB_WEBHOOK_SECRET": "MARKER-webhook",
		"ARCHFIT_GITHUB_OAUTH_SECRET":   "MARKER-oauth",
		"ARCHFIT_COOKIE_KEY":            "MARKER-cookie",
		"ARCHFIT_OPERATOR_TOKEN":        "MARKER-operator",
		"ARCHFIT_ANTHROPIC_KEY":         "MARKER-anthropic",
	}

	t.Run("inline values do not satisfy the file variables", func(t *testing.T) {
		env := map[string]string{envBaseURL: "https://archfit.example", envAppID: "12345"}
		for k, v := range inline {
			env[k] = v
		}
		_, err := Load(mapEnv(env))
		if err == nil || !strings.Contains(err.Error(), envPrivateKeyFile) {
			t.Fatalf("Load error = %v, want one naming %s", err, envPrivateKeyFile)
		}
	})

	t.Run("inline values are never read", func(t *testing.T) {
		env := requiredEnv()
		for k, v := range inline {
			env[k] = v
		}
		var asked []string
		c, err := Load(func(k string) string {
			asked = append(asked, k)
			return env[k]
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		for _, k := range asked {
			if _, isInline := inline[k]; isInline {
				t.Errorf("Load read %s", k)
			}
		}
		if c.AnthropicKeyFile != "" {
			t.Errorf("AnthropicKeyFile = %q, want empty", c.AnthropicKeyFile)
		}
	})
}
