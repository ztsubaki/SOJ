package rpcutil

import sojv1 "github.com/ztsubaki/soj/proto/soj/v1"

func ExecutorProto(value string) sojv1.AgentExecutor {
	switch value {
	case "slurm":
		return sojv1.AgentExecutor_AGENT_EXECUTOR_SLURM
	case "docker":
		return sojv1.AgentExecutor_AGENT_EXECUTOR_DOCKER
	default:
		return sojv1.AgentExecutor_AGENT_EXECUTOR_UNSPECIFIED
	}
}

func ExecutorText(value sojv1.AgentExecutor) string {
	switch value {
	case sojv1.AgentExecutor_AGENT_EXECUTOR_SLURM:
		return "slurm"
	case sojv1.AgentExecutor_AGENT_EXECUTOR_DOCKER:
		return "docker"
	default:
		return ""
	}
}
