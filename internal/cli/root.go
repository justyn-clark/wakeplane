package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	root.AddCommand(newScheduleCmd(&baseURL))
	root.AddCommand(newRunCmd(&baseURL))
	return root
}

func newServeCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the Wakeplane daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			cfg := config.FromEnv(version)
			service, err := app.New(ctx, cfg)
			if err != nil {
				return err
			}
			defer service.Close()

			server := &http.Server{
				Addr:    cfg.HTTPAddress,
				Handler: api.NewMux(service),
			}
			go func() {
				<-ctx.Done()
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = server.Shutdown(shutdownCtx)
			}()
			go func() {
				_ = service.Run(ctx)
			}()

			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				return err
			}
			return nil
		},
	}
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

	cmd.AddCommand(create, list, get, pause, resume, del, trigger)
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
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return do(req)
}

func do(req *http.Request) error {
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
