package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"

	"github.com/justyn-clark/wakeplane/internal/api"
	"github.com/justyn-clark/wakeplane/internal/app"
	"github.com/justyn-clark/wakeplane/internal/config"
	"github.com/justyn-clark/wakeplane/internal/domain"
	"github.com/spf13/cobra"
)

func NewRootCmd(version string) *cobra.Command {
	baseURL := "http://127.0.0.1:8080"
	root := &cobra.Command{
		Use:   "wakeplane",
		Short: "Durable scheduling and automated execution engine",
	}
	root.PersistentFlags().StringVar(&baseURL, "addr", baseURL, "Wakeplane HTTP base URL")
	root.AddCommand(newServeCmd(version))
	root.AddCommand(newStatusCmd(&baseURL))
	root.AddCommand(newScheduleCmd(&baseURL))
	root.AddCommand(newRunCmd(&baseURL))
	root.AddCommand(newVersionCmd(version))
	return root
}

func newStatusCmd(baseURL *string) *cobra.Command {
	var watch bool
	var everySeconds int
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show operational status",
		RunE: func(cmd *cobra.Command, args []string) error {
			if everySeconds <= 0 {
				everySeconds = 2
			}
			for {
				if err := printStatus(*baseURL); err != nil {
					return err
				}
				if !watch {
					return nil
				}
				time.Sleep(time.Duration(everySeconds) * time.Second)
			}
		},
	}
	cmd.Flags().BoolVarP(&watch, "watch", "w", false, "Refresh status continuously")
	cmd.Flags().IntVar(&everySeconds, "every", 2, "Watch refresh interval in seconds")
	return cmd
}

func newVersionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Wakeplane version",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version)
			return err
		},
	}
}

func newServeCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the Wakeplane daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			cfg := config.FromEnv(version)
			return runServe(ctx, cfg, app.New, serveHooks{})
		},
	}
}

type serveHooks struct {
	onListening func(addr string)
}

func runServe(ctx context.Context, cfg config.Config, newService func(context.Context, config.Config) (*app.Service, error), hooks serveHooks) error {
	logger := slog.Default()
	service, err := newService(ctx, cfg)
	if err != nil {
		return err
	}
	defer service.Close()

	listener, err := net.Listen("tcp", cfg.HTTPAddress)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: api.NewMux(service)}
	logger.Info("listening", "addr", listener.Addr().String())
	if hooks.onListening != nil {
		hooks.onListening(listener.Addr().String())
	}

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		<-groupCtx.Done()
		logger.Info("signal received, shutting down HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return nil
	})
	group.Go(func() error {
		if err := service.Run(groupCtx); err != nil && err != context.Canceled {
			return err
		}
		return nil
	})
	group.Go(func() error {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	})
	if err := group.Wait(); err != nil {
		logger.Error("serve exited with error", "error", err)
		return err
	}
	logger.Info("serve stopped cleanly")
	return nil
}

func newScheduleCmd(baseURL *string) *cobra.Command {
	cmd := &cobra.Command{Use: "schedule", Short: "Manage schedules"}

	var manifest string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a schedule from YAML",
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := os.ReadFile(manifest)
			if err != nil {
				return err
			}
			var req domain.CreateScheduleRequest
			if err := yaml.Unmarshal(b, &req); err != nil {
				return err
			}
			return postJSON(*baseURL+"/v1/schedules", req)
		},
	}
	create.Flags().StringVarP(&manifest, "file", "f", "", "Schedule manifest")
	_ = create.MarkFlagRequired("file")

	update := &cobra.Command{
		Use:   "update <id>",
		Short: "Replace a schedule from YAML",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := os.ReadFile(manifest)
			if err != nil {
				return err
			}
			var req domain.UpdateScheduleRequest
			if err := yaml.Unmarshal(b, &req); err != nil {
				return err
			}
			return putJSON(*baseURL+"/v1/schedules/"+args[0], req)
		},
	}
	update.Flags().StringVarP(&manifest, "file", "f", "", "Schedule manifest")
	_ = update.MarkFlagRequired("file")

	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Create schedules from a YAML file",
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := os.ReadFile(manifest)
			if err != nil {
				return err
			}
			var batch struct {
				Schedules []domain.CreateScheduleRequest `yaml:"schedules"`
			}
			if err := yaml.Unmarshal(b, &batch); err != nil {
				return err
			}
			if len(batch.Schedules) == 0 {
				var single domain.CreateScheduleRequest
				if err := yaml.Unmarshal(b, &single); err != nil {
					return err
				}
				batch.Schedules = []domain.CreateScheduleRequest{single}
			}
			for _, req := range batch.Schedules {
				if err := postJSON(*baseURL+"/v1/schedules", req); err != nil {
					return err
				}
			}
			return nil
		},
	}
	importCmd.Flags().StringVarP(&manifest, "file", "f", "", "Schedule manifest")
	_ = importCmd.MarkFlagRequired("file")

	export := &cobra.Command{Use: "export", Short: "Export schedules as JSON", RunE: func(cmd *cobra.Command, args []string) error {
		return getAndPrint(*baseURL + "/v1/schedules?limit=1000")
	}}
	list := &cobra.Command{Use: "list", Short: "List schedules", RunE: func(cmd *cobra.Command, args []string) error {
		return getAndPrint(*baseURL + "/v1/schedules")
	}}
	get := &cobra.Command{Use: "get <id>", Short: "Get one schedule", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return getAndPrint(*baseURL + "/v1/schedules/" + args[0])
	}}
	pause := &cobra.Command{Use: "pause <id>", Short: "Pause a schedule", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return postJSON(*baseURL+"/v1/schedules/"+args[0]+"/pause", map[string]any{})
	}}
	resume := &cobra.Command{Use: "resume <id>", Short: "Resume a schedule", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return postJSON(*baseURL+"/v1/schedules/"+args[0]+"/resume", map[string]any{})
	}}
	del := &cobra.Command{Use: "delete <id>", Short: "Delete a schedule", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		req, _ := http.NewRequest(http.MethodDelete, *baseURL+"/v1/schedules/"+args[0], nil)
		return do(req)
	}}
	trigger := &cobra.Command{Use: "trigger <id>", Short: "Trigger a schedule now", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return postJSON(*baseURL+"/v1/schedules/"+args[0]+"/trigger", domain.TriggerRequest{Reason: "manual operator trigger"})
	}}

	cmd.AddCommand(create, update, importCmd, export, list, get, pause, resume, del, trigger)
	return cmd
}

func newRunCmd(baseURL *string) *cobra.Command {
	cmd := &cobra.Command{Use: "run", Short: "Inspect runs"}
	list := &cobra.Command{Use: "list", Short: "List runs", RunE: func(cmd *cobra.Command, args []string) error {
		return getAndPrint(*baseURL + "/v1/runs")
	}}
	get := &cobra.Command{Use: "get <id>", Short: "Get one run", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return getAndPrint(*baseURL + "/v1/runs/" + args[0])
	}}
	cmd.AddCommand(list, get)
	return cmd
}

func getAndPrint(url string) error {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	return do(req)
}

func postJSON(url string, body any) error {
	return sendJSON(http.MethodPost, url, body)
}

func putJSON(url string, body any) error {
	return sendJSON(http.MethodPut, url, body)
}

func sendJSON(method, url string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return do(req)
}

func do(req *http.Request) error {
	applyAuth(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s", body)
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, body, "", "  ") == nil {
		_, _ = os.Stdout.Write(pretty.Bytes())
		_, _ = os.Stdout.Write([]byte("\n"))
		return nil
	}
	_, _ = os.Stdout.Write(body)
	return nil
}

func printStatus(baseURL string) error {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/v1/status", nil)
	if err != nil {
		return err
	}
	applyAuth(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s", body)
	}
	var status struct {
		Service   string `json:"service"`
		Version   string `json:"version"`
		StartedAt string `json:"started_at"`
		Scheduler struct {
			LastTickAt        string `json:"last_tick_at"`
			DueRuns           int    `json:"due_runs"`
			NextDueScheduleID string `json:"next_due_schedule_id"`
			NextDueRunAt      string `json:"next_due_run_at"`
		} `json:"scheduler"`
		Workers struct {
			Active            int `json:"active"`
			ClaimedButExpired int `json:"claimed_but_expired"`
		} `json:"workers"`
		Runs struct {
			Running     int `json:"running"`
			Failed      int `json:"failed"`
			RetryQueued int `json:"retry_queued"`
			DeadLetter  int `json:"dead_letter"`
		} `json:"runs"`
		Retention struct {
			RunRetentionDays int `json:"run_retention_days"`
			ReceiptMaxBytes  int `json:"receipt_max_bytes"`
		} `json:"retention"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return err
	}
	nextDue := "none"
	if status.Scheduler.NextDueRunAt != "" {
		nextDue = status.Scheduler.NextDueRunAt + " " + status.Scheduler.NextDueScheduleID
	}
	_, err = fmt.Fprintf(os.Stdout,
		"wakeplane %s started=%s last_tick=%s\nruns due=%d running=%d failed=%d retry_queued=%d dead_letter=%d workers active=%d expired_claims=%d\nnext_due=%s retention_days=%d receipt_max_bytes=%d\n",
		status.Version,
		status.StartedAt,
		status.Scheduler.LastTickAt,
		status.Scheduler.DueRuns,
		status.Runs.Running,
		status.Runs.Failed,
		status.Runs.RetryQueued,
		status.Runs.DeadLetter,
		status.Workers.Active,
		status.Workers.ClaimedButExpired,
		nextDue,
		status.Retention.RunRetentionDays,
		status.Retention.ReceiptMaxBytes,
	)
	return err
}

func applyAuth(req *http.Request) {
	if token := os.Getenv("WAKEPLANE_AUTH_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}
