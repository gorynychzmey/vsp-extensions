package tempreport

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	openrfc "github.com/oisee/open-rfc-go/rfc"

	"github.com/oisee/vibing-steampunk/pkg/mcpext"
	"github.com/oisee/vibing-steampunk/pkg/saprfc"
)

// jobWait is how long a generated report may run. They take a few seconds;
// the margin covers a busy batch queue.
const jobWait = 3 * time.Minute

// RFCRunner runs the generated reports over RFC: XBP for the background job,
// RFC_READ_TABLE for the tables.
type RFCRunner struct {
	C *openrfc.Client
}

func (r RFCRunner) RunReport(ctx context.Context, program string) ([]string, error) {
	run, err := saprfc.RunReportWith(ctx, r.C, saprfc.ReportRequest{Report: program, JobName: "VSP_" + program, Wait: jobWait})
	if err != nil {
		// After the start the job may still be queued or running and need
		// its report, however the wait for it ended.
		if run != nil && run.Started {
			return nil, fmt.Errorf("job %s / %s started, but its end is unknown (%v): %w", run.JobName, run.JobCount, err, ErrJobNotEnded)
		}
		return nil, err
	}
	if run.Status != "F" && run.Status != "A" {
		return nil, fmt.Errorf("job %s / %s has not ended after %s (status %s), so check SM37 before retrying: %w",
			run.JobName, run.JobCount, jobWait, run.StatusFor, ErrJobNotEnded)
	}
	log, err := saprfc.ReadJobLog(ctx, r.C, run.JobName, run.JobCount)
	if err != nil {
		return nil, fmt.Errorf("job %s / %s ended (%s), but its log could not be read: %w", run.JobName, run.JobCount, run.StatusFor, err)
	}
	lines := make([]string, 0, len(log))
	for _, e := range log {
		lines = append(lines, e.Text)
	}
	return lines, nil
}

func (r RFCRunner) ReadTable(ctx context.Context, table, where string, fields []string, max int) ([]map[string]string, error) {
	return saprfc.ReadTable(ctx, r.C, table, where, fields, max)
}

// Outcome is what an operation on a runner returns besides its error: the
// result to render, and the temporary report and warnings to name when it
// failed.
type Outcome struct {
	Result   any
	Program  string
	Warnings []string
}

// Respond hands fn a runner on env's RFC connection and renders what it
// returns: the result as JSON, or the error with the temporary report and the
// warnings. A broken connection is dropped, so the next call opens a new one.
func Respond(ctx context.Context, env mcpext.Env, what string, fn func(Runner) (Outcome, error)) (*mcp.CallToolResult, error) {
	c, release, err := env.RFC(ctx)
	if err != nil {
		return mcpext.Errorf("%s need the RFC connection: %v", what, err), nil
	}
	defer release()
	out, err := fn(RFCRunner{C: c})
	if err != nil {
		if errors.Is(err, openrfc.ErrTransport) || errors.Is(err, openrfc.ErrClosed) {
			env.DropRFC(ctx)
		}
		msg := err.Error()
		if out.Program != "" {
			msg = fmt.Sprintf("%s (temporary report %s)", msg, out.Program)
		}
		if len(out.Warnings) > 0 {
			msg += "\nWarnings:\n- " + strings.Join(out.Warnings, "\n- ")
		}
		return mcpext.Errorf("%s", msg), nil
	}
	return mcpext.JSON(out.Result), nil
}
