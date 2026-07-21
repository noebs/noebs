package main

import (
	"context"
	"errors"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	walletworker "github.com/adonese/noebs/wallet/worker"
)

func walletScheduleConfig() ebs_fields.NoebsConfig {
	return ebs_fields.NoebsConfig{
		WalletFXRefreshCron:               "30 16 * * 1-5",
		WalletPSPPollerCron:               "*/5 * * * *",
		WalletPSPPollerBatchSize:          100,
		WalletPSPPollerIntervalSeconds:    300,
		WalletReconciliationCron:          "0 3 * * *",
		WalletReconciliationBatchSize:     500,
		WalletReconciliationLookbackHours: 24,
	}
}

func TestStartCronWorkflowRequiresExplicitInputs(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		workflowID string
		cron       string
		taskQueue  walletworker.TaskQueue
		workflowFn interface{}
		want       error
	}{
		{name: "workflow id", cron: "* * * * *", taskQueue: walletworker.TaskQueueMain, workflowFn: func() {}, want: errMissingWalletWorkflowID},
		{name: "cron", workflowID: "workflow-id", taskQueue: walletworker.TaskQueueMain, workflowFn: func() {}, want: errMissingWalletWorkflowCron},
		{name: "task queue", workflowID: "workflow-id", cron: "* * * * *", workflowFn: func() {}, want: walletworker.ErrMissingTaskQueue},
		{name: "workflow fn", workflowID: "workflow-id", cron: "* * * * *", taskQueue: walletworker.TaskQueueMain, want: errMissingWalletWorkflow},
		{name: "temporal client", workflowID: "workflow-id", cron: "* * * * *", taskQueue: walletworker.TaskQueueMain, workflowFn: func() {}, want: errMissingWalletWorkflowClient},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := startCronWorkflow(ctx, nil, tt.workflowID, tt.cron, tt.taskQueue, tt.workflowFn)
			if !errors.Is(err, tt.want) {
				t.Fatalf("startCronWorkflow() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestStartWalletCronWorkflowsDoesNotFallbackToDefaultTenant(t *testing.T) {
	cfg := walletScheduleConfig()
	cfg.DefaultTenantID = "test-tenant"

	err := startWalletCronWorkflows(context.Background(), nil, nil, cfg, walletworker.TaskQueueMain)
	if !errors.Is(err, errMissingWalletTenants) {
		t.Fatalf("empty tenants error = %v, want %v", err, errMissingWalletTenants)
	}

	err = startWalletCronWorkflows(context.Background(), nil, []string{""}, cfg, walletworker.TaskQueueMain)
	if !errors.Is(err, store.ErrMissingTenantID) {
		t.Fatalf("blank tenant error = %v, want %v", err, store.ErrMissingTenantID)
	}

	err = startWalletCronWorkflows(context.Background(), nil, []string{"default"}, cfg, walletworker.TaskQueueMain)
	if !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("reserved tenant error = %v, want %v", err, store.ErrInvalidTenantID)
	}
}

func TestStartWalletCronWorkflowsRequiresConfiguredSchedules(t *testing.T) {
	cfg := walletScheduleConfig()
	cfg.WalletFXRefreshCron = ""

	err := startWalletCronWorkflows(context.Background(), nil, []string{"test-tenant"}, cfg, walletworker.TaskQueueMain)
	if !errors.Is(err, errMissingWalletWorkflowCron) {
		t.Fatalf("FX refresh cron error = %v, want %v", err, errMissingWalletWorkflowCron)
	}

	cfg = walletScheduleConfig()
	cfg.WalletPSPPollerCron = ""

	err = startWalletCronWorkflows(context.Background(), nil, []string{"test-tenant"}, cfg, walletworker.TaskQueueMain)
	if !errors.Is(err, errMissingWalletWorkflowCron) {
		t.Fatalf("poller cron error = %v, want %v", err, errMissingWalletWorkflowCron)
	}

	cfg = walletScheduleConfig()
	cfg.WalletReconciliationCron = ""
	err = startWalletCronWorkflows(context.Background(), nil, []string{"test-tenant"}, cfg, walletworker.TaskQueueMain)
	if !errors.Is(err, errMissingWalletWorkflowCron) {
		t.Fatalf("reconciliation cron error = %v, want %v", err, errMissingWalletWorkflowCron)
	}
}

func TestStartWalletCronWorkflowsRequiresRuntimeDependencies(t *testing.T) {
	cfg := walletScheduleConfig()

	err := startWalletCronWorkflows(context.Background(), nil, []string{"test-tenant"}, cfg, "")
	if !errors.Is(err, walletworker.ErrMissingTaskQueue) {
		t.Fatalf("task queue error = %v, want %v", err, walletworker.ErrMissingTaskQueue)
	}

	err = startWalletCronWorkflows(context.Background(), nil, []string{"test-tenant"}, cfg, walletworker.TaskQueueMain)
	if !errors.Is(err, errMissingWalletWorkflowClient) {
		t.Fatalf("temporal client error = %v, want %v", err, errMissingWalletWorkflowClient)
	}
}
