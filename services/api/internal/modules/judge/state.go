package judge

import (
	"strings"

	sojv1 "github.com/ztsubaki/soj/proto/soj/v1"
)

func terminalStateValue(value sojv1.ExecutionTerminalState) string {
	switch value {
	case sojv1.ExecutionTerminalState_EXECUTION_TERMINAL_STATE_COMPLETED:
		return "COMPLETED"
	case sojv1.ExecutionTerminalState_EXECUTION_TERMINAL_STATE_FAILED:
		return "FAILED"
	case sojv1.ExecutionTerminalState_EXECUTION_TERMINAL_STATE_TIMEOUT:
		return "TIMEOUT"
	case sojv1.ExecutionTerminalState_EXECUTION_TERMINAL_STATE_OUT_OF_MEMORY:
		return "OUT_OF_MEMORY"
	case sojv1.ExecutionTerminalState_EXECUTION_TERMINAL_STATE_CANCELLED:
		return "CANCELLED"
	default:
		return ""
	}
}

func activeState(value string) bool {
	return value == "claimed" || value == "submitted" || value == "running" || value == "finishing"
}

func stateOrder(value string) int {
	switch value {
	case "pending":
		return 0
	case "claimed":
		return 1
	case "submitted":
		return 2
	case "running":
		return 3
	case "finishing":
		return 4
	case "completed":
		return 5
	case "cancelled":
		return 6
	case "internal_error":
		return 7
	default:
		return -1
	}
}

func protoState(value sojv1.JobState) string {
	switch value {
	case sojv1.JobState_JOB_STATE_CLAIMED:
		return "claimed"
	case sojv1.JobState_JOB_STATE_SUBMITTED:
		return "submitted"
	case sojv1.JobState_JOB_STATE_RUNNING:
		return "running"
	case sojv1.JobState_JOB_STATE_FINISHING:
		return "finishing"
	default:
		return ""
	}
}

func toProtoState(value string) sojv1.JobState {
	switch value {
	case "claimed":
		return sojv1.JobState_JOB_STATE_CLAIMED
	case "submitted":
		return sojv1.JobState_JOB_STATE_SUBMITTED
	case "running":
		return sojv1.JobState_JOB_STATE_RUNNING
	case "finishing":
		return sojv1.JobState_JOB_STATE_FINISHING
	case "completed":
		return sojv1.JobState_JOB_STATE_COMPLETED
	case "cancelled":
		return sojv1.JobState_JOB_STATE_CANCELLED
	default:
		return sojv1.JobState_JOB_STATE_INTERNAL_ERROR
	}
}

func isASCII(value string) bool {
	return value != "" && len(value) <= 255 && strings.IndexFunc(value, func(r rune) bool { return r < 0x21 || r > 0x7e }) < 0
}

func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if value[i] != '-' {
				return false
			}
			continue
		}
		if !((value[i] >= '0' && value[i] <= '9') || (value[i] >= 'a' && value[i] <= 'f') || (value[i] >= 'A' && value[i] <= 'F')) {
			return false
		}
	}
	return true
}

func validAgentJob(agentID string, jobID int64) bool {
	return isUUID(agentID) && jobID > 0
}
