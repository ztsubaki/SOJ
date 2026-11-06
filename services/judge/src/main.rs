use anyhow::{bail, Context, Result};
use futures_util::{FutureExt, StreamExt};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
#[cfg(unix)]
use std::os::unix::fs::OpenOptionsExt;
use std::{
    collections::{HashMap, HashSet},
    future::Future,
    io::Read,
    net::IpAddr,
    path::Component,
    path::{Path, PathBuf},
    process::{Output, Stdio},
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};
use tokio::{
    fs::File,
    io::{AsyncReadExt, AsyncWriteExt},
    process::Command,
    sync::{watch, Mutex},
    task::{JoinHandle, JoinSet},
    time::{interval, timeout, MissedTickBehavior},
};
use tonic::{
    metadata::{Ascii, MetadataValue},
    transport::{Channel, ClientTlsConfig, Endpoint},
    Request,
};
use uuid::Uuid;

pub mod soj {
    pub mod v1 {
        tonic::include_proto!("soj.v1");
    }
    pub mod judge {
        pub mod v1 {
            tonic::include_proto!("soj.judge.v1");
        }
    }
}

use soj::{
    judge::v1::{
        judge_control_service_client::JudgeControlServiceClient, CancelJobRequest,
        ClaimJobsRequest, CompleteJobRequest, FailJobRequest, JobAssignment, RegisterAgentRequest,
        ReportAgentStatusRequest, ReportJobRequest, StructuredResult,
    },
    v1::{AgentExecutor, AgentState, ExecutionTerminalState, JobState, Verdict},
};

mod docker;

const RPC_TIMEOUT: Duration = Duration::from_secs(8);
const ARTIFACT_CONNECT_TIMEOUT: Duration = Duration::from_secs(10);
const ARTIFACT_READ_TIMEOUT: Duration = Duration::from_secs(30);
const ARTIFACT_UPLOAD_TIMEOUT: Duration = Duration::from_secs(120);
const MAX_UNPACKED_PACKAGE_BYTES: u64 = 10 << 30;
const MAX_GRPC_MESSAGE_BYTES: usize = 64 << 20;
const MAX_PACKAGE_ENTRIES: u64 = 10_000;
const MAX_RESULT_BYTES: u64 = 1 << 20;
const MAX_LOG_BYTES: u64 = 64 << 20;
const MAX_LOG_COMPONENT_BYTES: u64 = 8 << 20;
// Leave room for the unpacked package plus source and execution outputs.
const MAX_WORKSPACE_BYTES: u64 = 20 << 30;
// Count every descendant, including directories and links, but not the root.
const MAX_WORKSPACE_ENTRIES: u64 = 20_000;
const MAX_WORKSPACE_DEPTH: usize = 64;
const WORKSPACE_CHECK_INTERVAL: Duration = Duration::from_secs(5);
const SLURM_CLI_TIMEOUT: Duration = Duration::from_secs(10);
const ACCOUNTING_RETRY_INITIAL: Duration = Duration::from_secs(1);
const ACCOUNTING_RETRY_MAX: Duration = Duration::from_secs(30);
const SCHEDULER_POLL: Duration = Duration::from_secs(1);
const IDLE_DIAGNOSTIC_INTERVAL: Duration = Duration::from_secs(30);

#[derive(Clone, Copy, Debug, Default, Deserialize, Eq, PartialEq)]
#[serde(rename_all = "lowercase")]
enum Executor {
    #[default]
    Slurm,
    Docker,
}

impl Executor {
    fn protocol(self) -> AgentExecutor {
        match self {
            Self::Slurm => AgentExecutor::Slurm,
            Self::Docker => AgentExecutor::Docker,
        }
    }
}

struct Config {
    session_id: String,
    partition: String,
    executor: Executor,
    docker_socket: PathBuf,
    grpc_endpoint: String,
    grpc_server_name: Option<String>,
    insecure_grpc_dev: bool,
    grpc_ca_file: Option<PathBuf>,
    token: String,
    control_plane_http_url: reqwest::Url,
    work_root: PathBuf,
    idle_poll: Duration,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct FileConfig {
    agent_session_id: Option<String>,
    agent_partition: String,
    #[serde(default)]
    agent_executor: Executor,
    docker_socket: Option<PathBuf>,
    grpc_addr: String,
    grpc_server_name: Option<String>,
    grpc_ca_file: Option<PathBuf>,
    agent_service_token: String,
    control_plane_http_url: String,
    judge_work_root: PathBuf,
    judge_idle_poll_ms: Option<u64>,
    #[serde(default)]
    grpc_insecure_dev: bool,
}

impl Config {
    fn from_file(path: &Path) -> Result<Self> {
        let raw = std::fs::read_to_string(path)
            .with_context(|| format!("read Judge Agent config {}", path.display()))?;
        let file: FileConfig = toml::from_str(&raw)
            .with_context(|| format!("parse Judge Agent config {}", path.display()))?;
        let session_id = file
            .agent_session_id
            .filter(|value| !value.trim().is_empty())
            .unwrap_or_else(|| Uuid::new_v4().to_string());
        Uuid::parse_str(&session_id).context("agent_session_id must be a UUID")?;
        let partition = file.agent_partition.trim().to_owned();
        if partition.is_empty() {
            bail!("agent_partition is required");
        }
        if !partition.is_ascii() {
            bail!("agent_partition must be ASCII");
        }
        let grpc_addr = file.grpc_addr.trim().to_owned();
        if grpc_addr.is_empty() {
            bail!("grpc_addr is required");
        }
        let legacy_insecure_dev = file.grpc_insecure_dev;
        if legacy_insecure_dev && !loopback(&grpc_addr) {
            bail!("grpc_insecure_dev requires a loopback grpc_addr");
        }
        if grpc_addr.starts_with("http://") || grpc_addr.starts_with("https://") {
            bail!("grpc_addr is host:port; gRPC uses HTTPS by default");
        }
        let grpc_endpoint = if legacy_insecure_dev {
            format!("http://{grpc_addr}")
        } else {
            format!("https://{grpc_addr}")
        };
        let grpc_server_name = file
            .grpc_server_name
            .map(|value| nonempty(value, "grpc_server_name"))
            .transpose()?;
        let grpc_ca_file = if legacy_insecure_dev {
            None
        } else {
            let path = file.grpc_ca_file.context("grpc_ca_file is required")?;
            if !path.is_absolute() {
                bail!("grpc_ca_file must be absolute");
            }
            Some(path)
        };
        let work_root = file.judge_work_root;
        if !work_root.is_absolute() {
            bail!("judge_work_root must be absolute");
        }
        let control_plane_http_url = reqwest::Url::parse(file.control_plane_http_url.trim())
            .context("control_plane_http_url must be an absolute URL")?;
        if control_plane_http_url.query().is_some()
            || control_plane_http_url.fragment().is_some()
            || control_plane_http_url.path() != "/"
            || !control_plane_http_url.username().is_empty()
            || control_plane_http_url.password().is_some()
            || (control_plane_http_url.scheme() != "https"
                && control_plane_http_url.scheme() != "http")
        {
            bail!("control_plane_http_url must be an HTTP or HTTPS origin");
        }
        let idle_poll = file
            .judge_idle_poll_ms
            .map(Duration::from_millis)
            .unwrap_or(Duration::from_secs(1));
        if idle_poll.is_zero() {
            bail!("judge_idle_poll_ms must be positive");
        }
        let docker_socket = file
            .docker_socket
            .unwrap_or_else(|| PathBuf::from("/var/run/docker.sock"));
        docker::validate_socket_path(&docker_socket)?;
        if file.agent_executor == Executor::Docker {
            docker::validate_mount_path(&work_root)?;
        }
        Ok(Self {
            session_id,
            partition,
            executor: file.agent_executor,
            docker_socket,
            grpc_endpoint,
            grpc_server_name,
            insecure_grpc_dev: legacy_insecure_dev,
            grpc_ca_file,
            token: nonempty(file.agent_service_token, "agent_service_token")?,
            control_plane_http_url,
            work_root,
            idle_poll,
        })
    }
}

fn nonempty(value: String, name: &str) -> Result<String> {
    let value = value.trim().to_owned();
    if value.is_empty() {
        bail!("{name} is required");
    }
    Ok(value)
}
fn loopback(address: &str) -> bool {
    let Some((host, _)) = address.rsplit_once(':') else {
        return false;
    };
    let host = host.trim_matches(|character| character == '[' || character == ']');
    host == "localhost" || host.parse::<IpAddr>().is_ok_and(|ip| ip.is_loopback())
}

#[cfg(unix)]
async fn wait_for_shutdown() -> Result<()> {
    let mut terminate = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
        .context("listen for SIGTERM")?;
    tokio::select! {
        result = tokio::signal::ctrl_c() => result.context("wait for shutdown signal"),
        _ = terminate.recv() => Ok(()),
    }
}

#[cfg(not(unix))]
async fn wait_for_shutdown() -> Result<()> {
    tokio::signal::ctrl_c()
        .await
        .context("wait for shutdown signal")
}

#[derive(Clone)]
struct Control {
    client: JudgeControlServiceClient<Channel>,
    bearer: MetadataValue<Ascii>,
    mutations: Arc<Mutex<()>>,
}
impl Control {
    async fn connect(config: &Config) -> Result<Self> {
        let mut endpoint = Endpoint::from_shared(config.grpc_endpoint.clone())?
            .connect_timeout(RPC_TIMEOUT)
            .timeout(RPC_TIMEOUT);
        if !config.insecure_grpc_dev {
            let ca_file = config
                .grpc_ca_file
                .as_ref()
                .context("grpc_ca_file is required")?;
            let ca = tokio::fs::read(ca_file)
                .await
                .with_context(|| format!("read grpc_ca_file {}", ca_file.display()))?;
            let tls =
                ClientTlsConfig::new().ca_certificate(tonic::transport::Certificate::from_pem(ca));
            let tls = if let Some(server_name) = &config.grpc_server_name {
                tls.domain_name(server_name.clone())
            } else {
                tls
            };
            endpoint = endpoint.tls_config(tls)?;
        }
        let bearer = format!("Bearer {}", config.token)
            .parse()
            .context("agent_service_token has invalid metadata")?;
        Ok(Self {
            client: JudgeControlServiceClient::new(endpoint.connect().await?)
                .max_decoding_message_size(MAX_GRPC_MESSAGE_BYTES)
                .max_encoding_message_size(MAX_GRPC_MESSAGE_BYTES),
            bearer,
            mutations: Arc::new(Mutex::new(())),
        })
    }
    fn request<T>(&self, body: T) -> Request<T> {
        let mut request = Request::new(body);
        request
            .metadata_mut()
            .insert("authorization", self.bearer.clone());
        request.set_timeout(RPC_TIMEOUT);
        request
    }
    async fn register(&mut self, config: &Config) -> Result<Duration> {
        let response = self
            .client
            .register_agent(self.request(RegisterAgentRequest {
                agent_session_id: config.session_id.clone(),
                partition: config.partition.clone(),
                executor: config.executor.protocol() as i32,
            }))
            .await?
            .into_inner();
        let session = response
            .session
            .context("RegisterAgent returned no session")?;
        if session.id != config.session_id
            || session.partition != config.partition
            || session.executor != config.executor.protocol() as i32
        {
            bail!("RegisterAgent returned a different session identity");
        }
        live(AgentState::try_from(session.state).unwrap_or(AgentState::Unspecified))?;
        let duration = Duration::from_millis(response.heartbeat_interval_ms);
        if duration.is_zero() {
            bail!("RegisterAgent returned a zero heartbeat interval")
        }
        Ok(duration)
    }
    async fn heartbeat(
        &mut self,
        id: &str,
        partition: &str,
        executor: Executor,
    ) -> Result<AgentStatus> {
        let response = self
            .client
            .report_agent_status(self.request(ReportAgentStatusRequest {
                agent_session_id: id.to_owned(),
            }))
            .await;
        let response = match response {
            Ok(response) => response.into_inner(),
            Err(error) if error.code() == tonic::Code::FailedPrecondition => {
                // Drain can end the session before this heartbeat arrives.
                // Idempotent registration reads its immutable lifecycle and
                // never reopens an ended/offline session.
                let session = self
                    .client
                    .register_agent(self.request(RegisterAgentRequest {
                        agent_session_id: id.to_owned(),
                        partition: partition.to_owned(),
                        executor: executor.protocol() as i32,
                    }))
                    .await?
                    .into_inner()
                    .session
                    .context("missing Agent session")?;
                if session.state == AgentState::Ended as i32 {
                    return Ok(AgentStatus {
                        state: AgentState::Ended,
                        cancellations: Arc::new(HashSet::new()),
                    });
                }
                return Err(error.into());
            }
            Err(error) => return Err(error.into()),
        };
        let state = AgentState::try_from(response.state).unwrap_or(AgentState::Unspecified);
        if state != AgentState::Ended {
            live(state)?;
        }
        Ok(AgentStatus {
            state,
            cancellations: Arc::new(response.cancel_job_ids.into_iter().collect()),
        })
    }
    async fn claim(&mut self, id: &str) -> Result<ClaimResult> {
        let _mutation = self.mutations.lock().await;
        let response = self
            .client
            .claim_jobs(self.request(ClaimJobsRequest {
                agent_session_id: id.to_owned(),
            }))
            .await?
            .into_inner();
        live(AgentState::try_from(response.state).unwrap_or(AgentState::Unspecified))?;
        Ok(ClaimResult {
            assignment: response.assignment,
            pending_job_count: response.pending_job_count,
            eligible_pending_job_count: response.eligible_pending_job_count,
        })
    }
    async fn report(
        &mut self,
        agent_id: &str,
        job_id: i64,
        state: JobState,
        execution_id: Option<&str>,
    ) -> Result<JobState> {
        let _mutation = self.mutations.lock().await;
        let response = self
            .client
            .report_job(self.request(ReportJobRequest {
                agent_session_id: agent_id.to_owned(),
                job_id,
                state: state as i32,
                execution_id: execution_id.map(ToOwned::to_owned),
                observed_at: Some(now_timestamp()?),
            }))
            .await?;
        Ok(JobState::try_from(response.into_inner().state).unwrap_or(JobState::Unspecified))
    }
    async fn complete(
        &mut self,
        agent_id: &str,
        job_id: i64,
        terminal: ExecutionTerminalState,
        result: StructuredResult,
        result_artifact_id: Option<i64>,
        log_artifact_id: Option<i64>,
    ) -> Result<JobState> {
        let _mutation = self.mutations.lock().await;
        let response = self
            .client
            .complete_job(self.request(CompleteJobRequest {
                agent_session_id: agent_id.to_owned(),
                job_id,
                execution_terminal_state: terminal as i32,
                result: Some(result),
                result_artifact_id,
                log_artifact_id,
                finished_at: Some(now_timestamp()?),
            }))
            .await?
            .into_inner();
        Ok(JobState::try_from(response.state).unwrap_or(JobState::Unspecified))
    }
    async fn cancel_job(&mut self, agent_id: &str, job_id: i64) -> Result<JobState> {
        let _mutation = self.mutations.lock().await;
        let response = self
            .client
            .cancel_job(self.request(CancelJobRequest {
                agent_session_id: agent_id.to_owned(),
                job_id,
                finished_at: Some(now_timestamp()?),
            }))
            .await?
            .into_inner();
        Ok(JobState::try_from(response.state).unwrap_or(JobState::Unspecified))
    }
    async fn fail(&mut self, agent_id: &str, job_id: i64, message: &str) -> Result<JobState> {
        let _mutation = self.mutations.lock().await;
        let response = self
            .client
            .fail_job(self.request(FailJobRequest {
                agent_session_id: agent_id.to_owned(),
                job_id,
                status_message: ascii_status(message, 1024),
                finished_at: Some(now_timestamp()?),
            }))
            .await?
            .into_inner();
        Ok(JobState::try_from(response.state).unwrap_or(JobState::Unspecified))
    }
}

struct ClaimResult {
    assignment: Option<JobAssignment>,
    pending_job_count: u32,
    eligible_pending_job_count: u32,
}

#[derive(Clone)]
struct AgentStatus {
    state: AgentState,
    cancellations: Arc<HashSet<i64>>,
}

async fn wait_for_cancellation(mut status: watch::Receiver<AgentStatus>, job_id: i64) {
    loop {
        if status.borrow_and_update().cancellations.contains(&job_id) {
            return;
        }
        if status.changed().await.is_err() {
            // A closed heartbeat stream is handled by the Agent supervisor;
            // it is not evidence that this Job has stopped writing.
            std::future::pending::<()>().await;
        }
    }
}

fn live(state: AgentState) -> Result<()> {
    match state {
        AgentState::Online | AgentState::Draining => Ok(()),
        AgentState::Offline | AgentState::Ended => {
            bail!("Control Plane marked this Agent {state:?}")
        }
        AgentState::Unspecified => bail!("Control Plane returned an unspecified Agent state"),
    }
}

/// The Agent only invokes deployment-provided Slurm CLI programs. Arguments
/// are never interpolated into a shell string, and no Slurm library/FFI is used.
struct SlurmCli;

#[derive(Debug)]
struct UnknownSubmittedJob;
impl std::fmt::Display for UnknownSubmittedJob {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("sbatch submission outcome is unknown; retaining workspace")
    }
}
impl std::error::Error for UnknownSubmittedJob {}

fn submitted_job_id(stdout: &[u8]) -> Result<String> {
    let raw = std::str::from_utf8(stdout).map_err(|_| UnknownSubmittedJob)?;
    let id = raw.trim().split(';').next().unwrap_or_default();
    if id.is_empty() || !id.bytes().all(|byte| byte.is_ascii_digit()) {
        return Err(UnknownSubmittedJob.into());
    }
    Ok(id.to_owned())
}

#[derive(Debug)]
struct CommandStarted;
impl std::fmt::Display for CommandStarted {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("Slurm command started but its output was not collected")
    }
}
impl std::error::Error for CommandStarted {}

#[derive(Debug)]
struct SlurmCommandTimeout(&'static str);
impl std::fmt::Display for SlurmCommandTimeout {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{} exceeded its CLI timeout", self.0)
    }
}
impl std::error::Error for SlurmCommandTimeout {}

async fn run_slurm_command(
    command: &mut Command,
    name: &'static str,
    deadline: Duration,
) -> Result<Output> {
    let mut child = command
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true)
        .spawn()
        .with_context(|| format!("run {name}"))?;
    let mut stdout_pipe = child.stdout.take().context(CommandStarted)?;
    let mut stderr_pipe = child.stderr.take().context(CommandStarted)?;
    let mut stdout = Vec::new();
    let mut stderr = Vec::new();
    let outcome = timeout(deadline, async {
        let (status, _, _) = tokio::try_join!(
            child.wait(),
            stdout_pipe.read_to_end(&mut stdout),
            stderr_pipe.read_to_end(&mut stderr),
        )?;
        Ok::<_, anyhow::Error>(status)
    })
    .await;
    let error = match outcome {
        Ok(Ok(status)) => {
            return Ok(Output {
                status,
                stdout,
                stderr,
            })
        }
        Ok(Err(error)) => error,
        Err(_) => SlurmCommandTimeout(name).into(),
    };
    // wait_with_output consumes Child; keep it here so timeout always kills
    // and reaps the actual CLI process before returning an uncertain outcome.
    let _ = child.start_kill();
    if let Err(reap_error) = child.wait().await {
        return Err(error
            .context(format!("reap failed Slurm command: {reap_error}"))
            .context(CommandStarted));
    }
    Err(error.context(CommandStarted))
}

struct AccountingRetry {
    next_attempt: tokio::time::Instant,
    delay: Duration,
}
impl AccountingRetry {
    fn new() -> Self {
        Self {
            next_attempt: tokio::time::Instant::now(),
            delay: ACCOUNTING_RETRY_INITIAL,
        }
    }
    fn due(&self) -> bool {
        tokio::time::Instant::now() >= self.next_attempt
    }
    fn observe(&mut self, state: Option<SchedulerState>) {
        if state.is_some() {
            *self = Self::new();
        } else {
            self.next_attempt = tokio::time::Instant::now() + self.delay;
            self.delay = (self.delay * 2).min(ACCOUNTING_RETRY_MAX);
        }
    }
}

impl SlurmCli {
    fn command(binary: &str) -> Command {
        let mut command = Command::new(binary);
        command.env_clear().env("PATH", "/usr/bin:/bin");
        command
    }
    async fn submit(partition: &str, work: &Path) -> Result<String> {
        let script = work.join("package/judge.sbatch");
        let mut command = Self::command("sbatch");
        command.args([
            "--parsable",
            "--partition",
            partition,
            "--chdir",
            work.to_str().context("work path is not UTF-8")?,
            "--output",
            work.join("batch.stdout")
                .to_str()
                .context("stdout path is not UTF-8")?,
            "--error",
            work.join("batch.stderr")
                .to_str()
                .context("stderr path is not UTF-8")?,
            script.to_str().context("batch script path is not UTF-8")?,
        ]);
        Self::submit_command(&mut command, SLURM_CLI_TIMEOUT).await
    }
    async fn submit_command(command: &mut Command, deadline: Duration) -> Result<String> {
        let output = run_slurm_command(command, "sbatch", deadline)
            .await
            .map_err(|error| {
                if error.downcast_ref::<CommandStarted>().is_some() {
                    error.context(UnknownSubmittedJob)
                } else {
                    error
                }
            })?;
        if output.status.code().is_none() {
            return Err(UnknownSubmittedJob.into());
        }
        if !output.status.success() {
            bail!(
                "sbatch failed: {}",
                String::from_utf8_lossy(&output.stderr).trim()
            )
        }
        submitted_job_id(&output.stdout)
    }
    async fn cancel(job_id: &str) -> Result<()> {
        let output = run_slurm_command(
            Self::command("scancel").arg(job_id),
            "scancel",
            SLURM_CLI_TIMEOUT,
        )
        .await?;
        if !output.status.success() {
            bail!(
                "scancel failed: {}",
                String::from_utf8_lossy(&output.stderr).trim()
            )
        }
        Ok(())
    }
    async fn status(job_id: &str, retry: &mut AccountingRetry) -> Result<Option<SchedulerState>> {
        let queue = run_slurm_command(
            Self::command("squeue").args(["--json", "--jobs", job_id]),
            "squeue",
            SLURM_CLI_TIMEOUT,
        )
        .await?;
        if !queue.status.success() {
            bail!(
                "squeue failed: {}",
                String::from_utf8_lossy(&queue.stderr).trim()
            );
        }
        if let Some(state) = parse_scheduler_json(&queue.stdout)? {
            retry.observe(Some(state));
            return Ok(Some(state));
        }
        if !retry.due() {
            return Ok(None);
        }
        let accounting = run_slurm_command(
            Self::command("sacct").args(["--json", "--jobs", job_id]),
            "sacct",
            SLURM_CLI_TIMEOUT,
        )
        .await?;
        if !accounting.status.success() {
            bail!(
                "sacct failed: {}",
                String::from_utf8_lossy(&accounting.stderr).trim()
            );
        }
        let state = parse_scheduler_json(&accounting.stdout)?;
        retry.observe(state);
        Ok(state)
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum SchedulerState {
    Submitted,
    Running,
    Finishing,
    Completed,
    Failed,
    Timeout,
    OutOfMemory,
    Cancelled,
    InfrastructureFailure,
}

fn parse_scheduler_json(bytes: &[u8]) -> Result<Option<SchedulerState>> {
    let value: serde_json::Value = serde_json::from_slice(bytes).context("invalid Slurm JSON")?;
    let jobs = value
        .get("jobs")
        .and_then(serde_json::Value::as_array)
        .context("Slurm JSON has no jobs array")?;
    let Some(job) = jobs.first() else {
        return Ok(None);
    };
    let values = job
        .get("job_state")
        .and_then(json_state_values)
        .or_else(|| {
            job.get("state")
                .and_then(|v| v.get("current"))
                .and_then(json_state_values)
        })
        .context("Slurm job has no state")?;
    let mut state = None;
    let mut completing = false;
    for value in values {
        let raw = value.as_str().context("Slurm job state is not a string")?;
        for token in raw
            .split(|c: char| c.is_ascii_whitespace() || c == '+')
            .filter(|token| !token.is_empty())
        {
            state.get_or_insert(token);
            completing |= token == "COMPLETING";
        }
    }
    // Slurm may report a terminal base state while epilog cleanup still runs.
    // Inspect every flag before allowing terminal reporting or workspace removal.
    if completing {
        return Ok(Some(SchedulerState::Finishing));
    }
    let state = state.context("Slurm job has no state")?;
    let parsed = match state {
        "PENDING" | "CONFIGURING" => SchedulerState::Submitted,
        "RUNNING" => SchedulerState::Running,
        "COMPLETED" => SchedulerState::Completed,
        "FAILED" => SchedulerState::Failed,
        "TIMEOUT" => SchedulerState::Timeout,
        "OUT_OF_MEMORY" => SchedulerState::OutOfMemory,
        "CANCELLED" => SchedulerState::Cancelled,
        "NODE_FAIL" | "PREEMPTED" | "BOOT_FAIL" => SchedulerState::InfrastructureFailure,
        _ => bail!("unsupported Slurm state {state}"),
    };
    Ok(Some(parsed))
}

fn json_state_values(value: &serde_json::Value) -> Option<&[serde_json::Value]> {
    match value {
        serde_json::Value::String(_) => Some(std::slice::from_ref(value)),
        serde_json::Value::Array(values)
            if values.first().is_some_and(serde_json::Value::is_string) =>
        {
            Some(values)
        }
        _ => None,
    }
}

#[derive(Debug, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Manifest {
    schema_version: u32,
    problem_id: i64,
    #[serde(default, deserialize_with = "docker::present_manifest")]
    docker: Option<docker::DockerManifest>,
}

impl Manifest {
    fn validate(&self) -> Result<()> {
        if self.schema_version != 1 || !(1..=9_007_199_254_740_991).contains(&self.problem_id) {
            bail!("invalid package manifest schemaVersion or problemId");
        }
        if let Some(docker) = &self.docker {
            docker.validate()?;
        }
        Ok(())
    }

    fn executor(&self) -> Executor {
        if self.docker.is_some() {
            Executor::Docker
        } else {
            Executor::Slurm
        }
    }
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct JobContext<'a> {
    schema_version: u32,
    job_id: i64,
    submission: SubmissionContext<'a>,
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct SubmissionContext<'a> {
    filename: &'a str,
    content_type: &'a str,
}

fn artifact_client() -> Result<reqwest::Client> {
    Ok(reqwest::Client::builder()
        .connect_timeout(ARTIFACT_CONNECT_TIMEOUT)
        .read_timeout(ARTIFACT_READ_TIMEOUT)
        .build()?)
}

async fn download_artifact(
    config: &Config,
    job_id: i64,
    artifact_id: i64,
    expected_sha256: &str,
    expected_size: u64,
    destination: &Path,
    cancellation: impl Future<Output = ()>,
) -> Result<()> {
    if job_id <= 0 || artifact_id <= 0 {
        bail!("invalid Artifact download identifier");
    }
    let url = config.control_plane_http_url.join(&format!(
        "/internal/v1/judge/jobs/{job_id}/artifacts/{artifact_id}"
    ))?;
    tokio::pin!(cancellation);
    let request = artifact_client()?
        .get(url)
        .bearer_auth(&config.token)
        .header("X-SOJ-Agent-Session", &config.session_id)
        .send();
    let response = tokio::select! {
        biased;
        _ = &mut cancellation => return Err(PreparationCancelled.into()),
        response = request => response?.error_for_status()?,
    };
    let outcome = async {
        let mut file = File::create(destination).await?;
        let mut hash = Sha256::new();
        let mut size = 0_u64;
        let mut stream = response.bytes_stream();
        loop {
            let chunk = tokio::select! {
                biased;
                _ = &mut cancellation => return Err(PreparationCancelled.into()),
                chunk = stream.next() => chunk,
            };
            let Some(chunk) = chunk else { break };
            let chunk = chunk?;
            size = size
                .checked_add(chunk.len() as u64)
                .context("Artifact size overflow")?;
            if size > expected_size {
                bail!("Artifact exceeds assigned size");
            }
            hash.update(&chunk);
            file.write_all(&chunk).await?;
        }
        file.flush().await?;
        let actual = format!("{:x}", hash.finalize());
        if size != expected_size || actual != expected_sha256 {
            bail!("Artifact checksum or size does not match assignment");
        }
        Ok(())
    }
    .await;
    if outcome.is_err() {
        let _ = tokio::fs::remove_file(destination).await;
    }
    outcome
}

#[derive(Debug)]
struct PreparationCancelled;

impl std::fmt::Display for PreparationCancelled {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str("assignment preparation cancelled")
    }
}

impl std::error::Error for PreparationCancelled {}

fn preparation_active(cancelled: &AtomicBool) -> Result<()> {
    if cancelled.load(Ordering::Acquire) {
        return Err(PreparationCancelled.into());
    }
    Ok(())
}

struct PackageReader<R> {
    inner: R,
    cancelled: Arc<AtomicBool>,
}

impl<R: Read> Read for PackageReader<R> {
    fn read(&mut self, bytes: &mut [u8]) -> std::io::Result<usize> {
        if self.cancelled.load(Ordering::Acquire) {
            return Err(std::io::Error::other(PreparationCancelled));
        }
        self.inner.read(bytes)
    }
}

async fn unpack_package(
    archive: PathBuf,
    package: PathBuf,
    cancelled: Arc<AtomicBool>,
) -> Result<()> {
    tokio::task::spawn_blocking(move || -> Result<()> {
        preparation_active(&cancelled)?;
        let file = std::fs::File::open(archive)?;
        let decoder = zstd::stream::read::Decoder::new(file)?;
        let mut tar = tar::Archive::new(PackageReader {
            inner: decoder,
            cancelled: Arc::clone(&cancelled),
        });
        let mut entries = tar.entries()?;
        let mut materialized = HashSet::new();
        let mut entry_count = 0_u64;
        let mut unpacked = 0_u64;
        while let Some(entry) = entries.next() {
            preparation_active(&cancelled)?;
            let mut entry = entry?;
            // Match Control Plane: count yielded files and directories, not
            // GNU/PAX framing records already handled by the tar library.
            entry_count += 1;
            if entry_count > MAX_PACKAGE_ENTRIES {
                bail!("package archive has too many entries");
            }
            unpacked = unpacked
                .checked_add(entry.size())
                .context("package size overflow")?;
            if unpacked > MAX_UNPACKED_PACKAGE_BYTES {
                bail!("package archive expands beyond the configured limit");
            }
            let path = entry.path()?;
            let depth = path.components().count();
            // package/ itself occupies one level of the Job workspace.
            if depth == 0 || depth >= MAX_WORKSPACE_DEPTH {
                bail!("package archive exceeds workspace depth limit");
            }
            if path
                .components()
                .any(|part| !matches!(part, Component::Normal(_)))
            {
                bail!("package archive contains an unsafe path");
            }
            let kind = entry.header().entry_type();
            if !(kind.is_file() || kind.is_dir()) {
                bail!("package archive contains a link or special file");
            }
            // tar creates omitted parent directories too. Bound those before
            // extraction rather than letting a small archive grow a huge tree.
            for ancestor in path.ancestors().filter(|path| !path.as_os_str().is_empty()) {
                if !materialized.contains(ancestor) {
                    materialized.insert(ancestor.to_owned());
                }
                if materialized.len() as u64 > MAX_WORKSPACE_ENTRIES {
                    bail!("package archive creates too many workspace entries");
                }
            }
            entry.unpack_in(&package)?;
        }
        Ok(())
    })
    .await??;
    Ok(())
}

async fn write_job_context(
    job_dir: &Path,
    job_id: i64,
    filename: &str,
    content_type: &str,
) -> Result<()> {
    if job_id <= 0
        || filename.is_empty()
        || filename.len() > 255
        || content_type.is_empty()
        || content_type.len() > 255
    {
        bail!("invalid job context metadata");
    }
    let payload = serde_json::to_vec(&JobContext {
        schema_version: 1,
        job_id,
        submission: SubmissionContext {
            filename,
            content_type,
        },
    })?;
    tokio::fs::write(job_dir.join("job.json"), payload).await?;
    Ok(())
}

fn now_timestamp() -> Result<prost_types::Timestamp> {
    let elapsed = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .context("system clock precedes Unix epoch")?;
    Ok(prost_types::Timestamp {
        seconds: elapsed.as_secs().try_into().context("timestamp overflow")?,
        nanos: elapsed.subsec_nanos() as i32,
    })
}

fn ascii_status(value: &str, limit: usize) -> String {
    let mut output = String::with_capacity(value.len().min(limit));
    for byte in value.bytes() {
        if (b' '..=b'~').contains(&byte) && output.len() < limit {
            output.push(byte as char);
        }
    }
    if output.is_empty() {
        "Judge Agent execution error".to_owned()
    } else {
        output
    }
}

fn assignment_manifest(assignment: &JobAssignment) -> Result<Manifest> {
    fn value(value: &prost_types::Value) -> Result<serde_json::Value> {
        use prost_types::value::Kind;
        Ok(
            match value.kind.as_ref().context("manifest has missing value")? {
                Kind::NullValue(_) => serde_json::Value::Null,
                Kind::BoolValue(value) => serde_json::Value::Bool(*value),
                Kind::StringValue(value) => serde_json::Value::String(value.clone()),
                Kind::NumberValue(value)
                    if value.is_finite()
                        && value.fract() == 0.0
                        && value.abs() <= 9_007_199_254_740_991.0 =>
                {
                    serde_json::Value::from(*value as i64)
                }
                Kind::NumberValue(_) => bail!("manifest numbers must be safe integers"),
                Kind::StructValue(object) => object_value(object)?,
                Kind::ListValue(_) => bail!("manifest cannot contain arrays"),
            },
        )
    }
    fn object_value(object: &prost_types::Struct) -> Result<serde_json::Value> {
        let fields = object
            .fields
            .iter()
            .map(|(key, field)| Ok((key.clone(), value(field)?)))
            .collect::<Result<serde_json::Map<String, serde_json::Value>>>()?;
        Ok(serde_json::Value::Object(fields))
    }
    let snapshot = assignment
        .manifest
        .as_ref()
        .context("assignment has no manifest")?;
    let manifest: Manifest =
        serde_json::from_value(object_value(snapshot)?).context("invalid assignment manifest")?;
    manifest.validate()?;
    if assignment.executor != manifest.executor().protocol() as i32 {
        bail!("assignment executor does not match manifest");
    }
    Ok(manifest)
}

async fn validate_package(package: &Path, expected: &Manifest) -> Result<()> {
    let manifest_path = package.join("manifest.json");
    let script_path = package.join(if expected.docker.is_some() {
        "judge.sh"
    } else {
        "judge.sbatch"
    });
    for path in [&manifest_path, &script_path] {
        let metadata = tokio::fs::symlink_metadata(path).await?;
        if metadata.file_type().is_symlink() || !metadata.is_file() {
            bail!("package required entry is not a regular file");
        }
        if path == &manifest_path && metadata.len() > 64 << 10 {
            bail!("package manifest is too large");
        }
    }
    let bytes = tokio::fs::read(&manifest_path).await?;
    let manifest: Manifest = serde_json::from_slice(&bytes).context("invalid package manifest")?;
    manifest.validate()?;
    if manifest != *expected {
        bail!("package manifest does not match assignment snapshot");
    }
    Ok(())
}

async fn create_job_dir(config: &Config, job_id: i64) -> Result<PathBuf> {
    if job_id <= 0 {
        bail!("invalid job ID");
    }
    let job = config.work_root.join(job_id.to_string());
    tokio::fs::create_dir(&job)
        .await
        .context("job workspace already exists or cannot be created")?;
    for subdir in ["package", "submission", "output", "tmp", "tmp/run-output"] {
        if let Err(error) = tokio::fs::create_dir_all(job.join(subdir)).await {
            cleanup_workspace(&job).await;
            return Err(error.into());
        }
    }
    Ok(job)
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ArchivedArtifact {
    artifact_id: i64,
    sha256: String,
    size_bytes: u64,
}

async fn upload_artifact(config: &Config, job_id: i64, kind: &str, bytes: Vec<u8>) -> Result<i64> {
    if bytes.is_empty() || bytes.len() as u64 > MAX_LOG_BYTES {
        bail!("invalid artifact upload size");
    }
    let url = config.control_plane_http_url.join(&format!(
        "/internal/v1/judge/jobs/{job_id}/artifacts?kind={kind}"
    ))?;
    let response = artifact_client()?
        .post(url)
        .bearer_auth(&config.token)
        .header("X-SOJ-Agent-Session", &config.session_id)
        .header(reqwest::header::CONTENT_TYPE, "application/octet-stream")
        .body(bytes)
        .timeout(ARTIFACT_UPLOAD_TIMEOUT)
        .send()
        .await?
        .error_for_status()?;
    let artifact: ArchivedArtifact = serde_json::from_slice(&response.bytes().await?)
        .context("invalid artifact upload response")?;
    if artifact.artifact_id <= 0
        || artifact.sha256.len() != 64
        || !artifact.sha256.bytes().all(|byte| byte.is_ascii_hexdigit())
        || artifact.size_bytes == 0
    {
        bail!("invalid artifact upload response");
    }
    Ok(artifact.artifact_id)
}

fn read_regular_file(work: &Path, relative: &Path, maximum: u64) -> Result<Option<Vec<u8>>> {
    if relative.is_absolute()
        || relative
            .components()
            .any(|part| !matches!(part, Component::Normal(_)))
    {
        bail!("invalid output path");
    }
    let path = work.join(relative);
    let parent = path.parent().context("output path has no parent")?;
    for directory in [work, parent] {
        let metadata = std::fs::symlink_metadata(directory)?;
        if metadata.file_type().is_symlink() || !metadata.is_dir() {
            bail!("output directory is not a regular directory");
        }
    }
    let metadata = match std::fs::symlink_metadata(&path) {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error.into()),
    };
    if metadata.file_type().is_symlink() || !metadata.is_file() {
        bail!("output file is not regular");
    }
    if metadata.len() > maximum {
        bail!("output file exceeds limit");
    }
    let mut file = std::fs::OpenOptions::new()
        .read(true)
        .custom_flags(libc::O_NOFOLLOW)
        .open(&path)?;
    let opened = file.metadata()?;
    if !opened.is_file() || opened.len() > maximum {
        bail!("output file changed while opening");
    }
    let mut bytes = Vec::with_capacity(opened.len() as usize);
    file.by_ref().take(maximum + 1).read_to_end(&mut bytes)?;
    if bytes.len() as u64 > maximum {
        bail!("output file exceeds limit");
    }
    Ok(Some(bytes))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct PackageResult {
    schema_version: u32,
    job_id: i64,
    verdict: PackageVerdict,
    score: String,
    #[serde(default, deserialize_with = "present_result_message")]
    message: Option<String>,
}

fn present_result_message<'de, D>(deserializer: D) -> std::result::Result<Option<String>, D::Error>
where
    D: serde::Deserializer<'de>,
{
    String::deserialize(deserializer).map(Some)
}
#[derive(Deserialize)]
#[serde(rename_all = "snake_case")]
enum PackageVerdict {
    Accepted,
    WrongAnswer,
    CompileError,
    RuntimeError,
}

fn decimal(value: &str) -> bool {
    let Some((integer, fractional)) = value.split_once('.') else {
        return value == "0"
            || (!value.starts_with('0')
                && !value.is_empty()
                && value.bytes().all(|byte| byte.is_ascii_digit()));
    };
    !integer.is_empty()
        && (integer == "0"
            || (!integer.starts_with('0') && integer.bytes().all(|byte| byte.is_ascii_digit())))
        && !fractional.is_empty()
        && fractional.bytes().all(|byte| byte.is_ascii_digit())
}

fn validate_result(bytes: &[u8], job_id: i64) -> Result<StructuredResult> {
    let result: PackageResult = serde_json::from_slice(bytes).context("invalid result JSON")?;
    if result.schema_version != 1
        || result.job_id != job_id
        || result.job_id <= 0
        || result.score.len() > 128
        || !decimal(&result.score)
    {
        bail!("result does not satisfy the result contract");
    }
    if let Some(message) = &result.message {
        if message.is_empty()
            || message.len() > 1024
            || !message.is_ascii()
            || message.bytes().any(|byte| !(b' '..=b'~').contains(&byte))
        {
            bail!("result has invalid message");
        }
    }
    let verdict = match result.verdict {
        PackageVerdict::Accepted => Verdict::Accepted,
        PackageVerdict::WrongAnswer => Verdict::WrongAnswer,
        PackageVerdict::CompileError => Verdict::CompileError,
        PackageVerdict::RuntimeError => Verdict::RuntimeError,
    };
    Ok(StructuredResult {
        verdict: verdict as i32,
        score: result.score,
        message: result.message,
    })
}

async fn collect_log(config: &Config, job_id: i64, work: &Path) -> Result<Option<i64>> {
    let work = work.to_owned();
    let combined = tokio::task::spawn_blocking(move || collect_log_bytes(job_id, &work)).await?;
    let Some(combined) = combined else {
        return Ok(None);
    };
    match upload_artifact(config, job_id, "judge_log", combined).await {
        Ok(id) => Ok(Some(id)),
        Err(error) => {
            eprintln!("warning: cannot upload judge log for {job_id}: {error:#}");
            Ok(None)
        }
    }
}

fn collect_log_bytes(job_id: i64, work: &Path) -> Option<Vec<u8>> {
    let mut combined = Vec::new();
    for (label, path) in [
        ("judge.log", Path::new("output/judge.log")),
        ("batch.stdout", Path::new("batch.stdout")),
        ("batch.stderr", Path::new("batch.stderr")),
        ("docker.log", Path::new("docker.log")),
    ] {
        match read_regular_file(work, path, MAX_LOG_COMPONENT_BYTES) {
            Ok(Some(bytes)) => {
                combined.extend_from_slice(format!("--- {label} ---\n").as_bytes());
                combined.extend_from_slice(&bytes);
                combined.push(b'\n');
            }
            Ok(None) => {}
            Err(error) => eprintln!("warning: cannot collect {label} for {job_id}: {error:#}"),
        }
    }
    if combined.is_empty() {
        return None;
    }
    if combined.len() as u64 > MAX_LOG_BYTES {
        eprintln!("warning: judge log for {job_id} exceeds limit");
        return None;
    }
    Some(combined)
}

#[derive(Clone, Copy, PartialEq)]
enum WorkspaceScan {
    Active,
    Final,
}

fn workspace_within_limit(work: &Path, scan: WorkspaceScan) -> Result<()> {
    let root = std::fs::symlink_metadata(work)?;
    if !root.is_dir() || root.file_type().is_symlink() {
        bail!("workspace root is not a directory");
    }
    let mut pending = vec![(work.to_owned(), 0_usize)];
    let mut bytes = 0_u64;
    let mut entries = 0_u64;
    while let Some((directory, depth)) = pending.pop() {
        // Only this directory iterator stays open; ancestors have already been
        // consumed and closed, so deep trees cannot exhaust file descriptors.
        let children = match std::fs::read_dir(&directory) {
            Ok(children) => children,
            Err(error)
                if scan == WorkspaceScan::Active
                    && depth > 0
                    && error.kind() == std::io::ErrorKind::NotFound =>
            {
                continue;
            }
            Err(error) => return Err(error.into()),
        };
        for entry in children {
            let entry = match entry {
                Ok(entry) => entry,
                Err(error)
                    if scan == WorkspaceScan::Active
                        && error.kind() == std::io::ErrorKind::NotFound =>
                {
                    continue;
                }
                Err(error) => return Err(error.into()),
            };
            let path = entry.path();
            let metadata = match std::fs::symlink_metadata(&path) {
                Ok(metadata) => metadata,
                Err(error)
                    if scan == WorkspaceScan::Active
                        && error.kind() == std::io::ErrorKind::NotFound =>
                {
                    continue;
                }
                Err(error) => return Err(error.into()),
            };
            entries = entries
                .checked_add(1)
                .context("workspace entry count overflow")?;
            if entries > MAX_WORKSPACE_ENTRIES || depth + 1 > MAX_WORKSPACE_DEPTH {
                bail!("workspace exceeds entry or depth limit");
            }
            if metadata.file_type().is_symlink() {
                continue;
            }
            if metadata.is_file() {
                bytes = bytes
                    .checked_add(metadata.len())
                    .context("workspace size overflow")?;
                if bytes > MAX_WORKSPACE_BYTES {
                    bail!("workspace exceeds byte limit");
                }
            } else if metadata.is_dir() {
                pending.push((path, depth + 1));
            } else {
                bail!("workspace contains a special file");
            }
        }
    }
    Ok(())
}

struct WorkspaceChecks {
    work: PathBuf,
    stop: watch::Sender<bool>,
    errors: watch::Receiver<Option<Arc<str>>>,
    task: Option<JoinHandle<Result<()>>>,
}
impl WorkspaceChecks {
    fn new(work: &Path) -> Self {
        let (stop, stopping) = watch::channel(false);
        let (errors, receiver) = watch::channel(None);
        let path = work.to_owned();
        let task = tokio::spawn(workspace_check_loop(stopping, errors, move || {
            let path = path.clone();
            async move {
                tokio::task::spawn_blocking(move || {
                    workspace_within_limit(&path, WorkspaceScan::Active)
                })
                .await?
            }
        }));
        Self {
            work: work.to_owned(),
            stop,
            errors: receiver,
            task: Some(task),
        }
    }
    async fn next(&mut self) -> Result<()> {
        self.errors
            .changed()
            .await
            .context("workspace checker stopped")?;
        let message = self.errors.borrow_and_update().clone();
        bail!(
            "{}",
            message.context("workspace checker returned no error")?
        );
    }
    async fn finish(&mut self) -> Result<()> {
        // Stop scheduling, then join the one in-flight scan before cleanup.
        let _ = self.stop.send(true);
        if let Some(task) = self.task.take() {
            task.await.context("workspace checker task failed")??;
        }
        if let Some(message) = self.errors.borrow().as_ref() {
            bail!("{message}");
        }
        let work = self.work.clone();
        tokio::task::spawn_blocking(move || workspace_within_limit(&work, WorkspaceScan::Final))
            .await?
    }
}

async fn workspace_check_loop<F, Fut>(
    mut stopping: watch::Receiver<bool>,
    errors: watch::Sender<Option<Arc<str>>>,
    mut scan: F,
) -> Result<()>
where
    F: FnMut() -> Fut,
    Fut: Future<Output = Result<()>>,
{
    let mut timer = interval(WORKSPACE_CHECK_INTERVAL);
    timer.set_missed_tick_behavior(MissedTickBehavior::Skip);
    loop {
        tokio::select! {
            biased;
            _ = stopping.changed() => return Ok(()),
            _ = timer.tick() => {}
        }
        // Await inside this independent task: no second scan can overlap it,
        // while cancellation, scheduler polling and Job RPCs remain unblocked.
        if let Err(error) = scan().await {
            errors.send_replace(Some(Arc::from(format!(
                "workspace limit violation: {error:#}"
            ))));
        }
    }
}

struct JobProgress {
    state: JobState,
    execution_id_recorded: bool,
}
impl JobProgress {
    fn needs_report(&self, state: JobState, has_execution_id: bool) -> bool {
        state as i32 > self.state as i32 || (has_execution_id && !self.execution_id_recorded)
    }
    fn acknowledge(&mut self, state: JobState, response: JobState, has_execution_id: bool) {
        if state as i32 > self.state as i32 {
            self.state = state;
        }
        if response as i32 > self.state as i32 {
            self.state = response;
        }
        self.execution_id_recorded |= has_execution_id;
    }
    async fn report(
        &mut self,
        control: &mut Control,
        config: &Config,
        job_id: i64,
        state: JobState,
        execution_id: &str,
        waiting: &AtomicBool,
    ) -> Result<()> {
        if self.needs_report(state, true) {
            let response = control
                .report(&config.session_id, job_id, state, Some(execution_id))
                .await?;
            self.acknowledge(state, response, true);
        }
        // The queue slot is released only after the advancing report succeeds.
        if self.state as i32 >= JobState::Running as i32 {
            waiting.store(false, Ordering::Release);
        }
        Ok(())
    }
}

fn artifact_reference<'a>(
    reference: Option<&'a soj::judge::v1::ArtifactReference>,
    name: &str,
) -> Result<(i64, &'a str, u64)> {
    let reference = reference.with_context(|| format!("assignment has no {name} artifact"))?;
    if reference.artifact_id <= 0
        || reference.sha256.len() != 64
        || !reference
            .sha256
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit())
        || reference.size_bytes == 0
    {
        bail!("assignment has invalid {name} artifact");
    }
    Ok((
        reference.artifact_id,
        &reference.sha256,
        reference.size_bytes,
    ))
}

async fn stage_assignment(
    config: &Config,
    assignment: &JobAssignment,
    status: watch::Receiver<AgentStatus>,
) -> Result<(PathBuf, Manifest)> {
    if status.borrow().cancellations.contains(&assignment.job_id) {
        return Err(PreparationCancelled.into());
    }
    if assignment.job_id <= 0
        || assignment.submission_id <= 0
        || assignment.agent_session_id != config.session_id
        || assignment.assigned_partition != config.partition
        || assignment.executor != config.executor.protocol() as i32
    {
        bail!("assignment identity does not match this Agent");
    }
    if assignment.submission_filename.is_empty()
        || assignment.submission_filename.len() > 255
        || assignment.submission_content_type.is_empty()
        || assignment.submission_content_type.len() > 255
    {
        bail!("assignment has invalid submission metadata");
    }
    let expected_manifest = assignment_manifest(assignment)?;
    let (package_id, package_sha, package_size) =
        artifact_reference(assignment.package.as_ref(), "package")?;
    let (submission_id, submission_sha, submission_size) =
        artifact_reference(assignment.submission.as_ref(), "submission")?;
    let work = create_job_dir(config, assignment.job_id).await?;
    let package_archive = work.join("package.tar.zst");
    let cancelled = Arc::new(AtomicBool::new(false));
    let cancellation = wait_for_cancellation(status, assignment.job_id).shared();
    let cancel_preparation = cancellation.clone();
    tokio::pin!(cancel_preparation);
    let staged = {
        let preparation = async {
            preparation_active(&cancelled)?;
            download_artifact(
                config,
                assignment.job_id,
                package_id,
                package_sha,
                package_size,
                &package_archive,
                cancellation.clone(),
            )
            .await?;
            preparation_active(&cancelled)?;
            unpack_package(
                package_archive.clone(),
                work.join("package"),
                Arc::clone(&cancelled),
            )
            .await?;
            preparation_active(&cancelled)?;
            validate_package(&work.join("package"), &expected_manifest).await?;
            download_artifact(
                config,
                assignment.job_id,
                submission_id,
                submission_sha,
                submission_size,
                &work.join("submission/source"),
                cancellation.clone(),
            )
            .await?;
            preparation_active(&cancelled)?;
            write_job_context(
                &work,
                assignment.job_id,
                &assignment.submission_filename,
                &assignment.submission_content_type,
            )
            .await?;
            preparation_active(&cancelled)?;
            let path = work.clone();
            tokio::task::spawn_blocking(move || workspace_within_limit(&path, WorkspaceScan::Final))
                .await?
        };
        tokio::pin!(preparation);
        tokio::select! {
            biased;
            _ = &mut cancel_preparation => {
                cancelled.store(true, Ordering::Release);
                // Never drop preparation: blocking extraction must stop and
                // join before cleanup can remove the files it was writing.
                let _ = preparation.await;
                Err(PreparationCancelled.into())
            }
            result = &mut preparation => result,
        }
    };
    let _ = tokio::fs::remove_file(package_archive).await;
    if let Err(error) = staged {
        cleanup_workspace(&work).await;
        return Err(error);
    }
    Ok((work, expected_manifest))
}

async fn terminal_complete(
    config: &Config,
    control: &mut Control,
    job_id: i64,
    work: &Path,
    terminal: ExecutionTerminalState,
    result: StructuredResult,
    result_bytes: Option<Vec<u8>>,
) -> Result<()> {
    let result_artifact_id = match result_bytes {
        Some(bytes) => Some(upload_artifact(config, job_id, "judge_result", bytes).await?),
        None => None,
    };
    let log_artifact_id = collect_log(config, job_id, work).await?;
    let state = control
        .complete(
            &config.session_id,
            job_id,
            terminal,
            result,
            result_artifact_id,
            log_artifact_id,
        )
        .await?;
    eprintln!("job {job_id} reached Control Plane state {state:?}");
    Ok(())
}

async fn finish_execution(
    config: &Config,
    control: &mut Control,
    job_id: i64,
    work: &Path,
    execution_id: &str,
    terminal: ExecutionTerminalState,
    waiting: &AtomicBool,
    progress: &mut JobProgress,
) -> Result<()> {
    if terminal == ExecutionTerminalState::Cancelled {
        let state = control.cancel_job(&config.session_id, job_id).await?;
        eprintln!("job {job_id} reached Control Plane state {state:?}");
        return Ok(());
    }
    progress
        .report(
            control,
            config,
            job_id,
            JobState::Finishing,
            execution_id,
            waiting,
        )
        .await?;
    if matches!(
        terminal,
        ExecutionTerminalState::Timeout | ExecutionTerminalState::OutOfMemory
    ) {
        return terminal_complete(
            config,
            control,
            job_id,
            work,
            terminal,
            StructuredResult {
                verdict: if terminal == ExecutionTerminalState::Timeout {
                    Verdict::TimeLimit as i32
                } else {
                    Verdict::MemoryLimit as i32
                },
                score: "0".to_owned(),
                message: None,
            },
            None,
        )
        .await;
    }
    let output_work = work.to_owned();
    let result_bytes = tokio::task::spawn_blocking(move || {
        read_regular_file(
            &output_work,
            Path::new("output/result.json"),
            MAX_RESULT_BYTES,
        )
    })
    .await?;
    let result_bytes = match result_bytes {
        Ok(Some(bytes)) => bytes,
        Ok(None) => {
            control
                .fail(
                    &config.session_id,
                    job_id,
                    "execution completed without output/result.json",
                )
                .await?;
            return Ok(());
        }
        Err(error) => {
            control
                .fail(
                    &config.session_id,
                    job_id,
                    &format!("invalid output/result.json: {error:#}"),
                )
                .await?;
            return Ok(());
        }
    };
    let result = match validate_result(&result_bytes, job_id) {
        Ok(result) => result,
        Err(error) => {
            control
                .fail(
                    &config.session_id,
                    job_id,
                    &format!("invalid output/result.json: {error:#}"),
                )
                .await?;
            return Ok(());
        }
    };
    terminal_complete(
        config,
        control,
        job_id,
        work,
        terminal,
        result,
        Some(result_bytes),
    )
    .await
}

async fn monitor_job(
    config: &Config,
    control: &mut Control,
    assignment: &JobAssignment,
    work: &Path,
    execution_id: &str,
    status: watch::Receiver<AgentStatus>,
    waiting: &AtomicBool,
    mut progress: JobProgress,
) -> Result<()> {
    let mut cancellation_requested = false;
    let cancellation = wait_for_cancellation(status.clone(), assignment.job_id);
    tokio::pin!(cancellation);
    let mut cancel_due = false;
    let mut workspace_error = None;
    let mut checks = WorkspaceChecks::new(work);
    let mut scheduler: JoinSet<(AccountingRetry, Result<Option<SchedulerState>>)> = JoinSet::new();
    let mut cancelling = JoinSet::new();
    let mut accounting = Some(AccountingRetry::new());
    let mut checks_open = true;
    let mut poll = interval(SCHEDULER_POLL);
    poll.set_missed_tick_behavior(MissedTickBehavior::Delay);
    loop {
        // Known cancellation always starts before handling any scan result.
        if cancel_due && cancelling.is_empty() {
            let id = execution_id.to_owned();
            cancelling.spawn(async move { SlurmCli::cancel(&id).await });
            cancel_due = false;
        }
        let state = tokio::select! {
            biased;
            _ = &mut cancellation, if !cancellation_requested => {
                // Stop polling the listener permanently once intent is latched.
                cancellation_requested = true;
                cancel_due = true;
                continue;
            }
            result = cancelling.join_next(), if !cancelling.is_empty() => {
                if let Err(error) = result.context("Slurm cancellation task disappeared")?? {
                    eprintln!("warning: cannot cancel Slurm job {execution_id}: {error:#}");
                }
                continue;
            }
            result = checks.next(), if checks_open => {
                checks_open = checks.errors.has_changed().is_ok();
                if let Err(error) = result {
                    workspace_error.get_or_insert_with(|| format!("{error:#}"));
                    cancel_due = true;
                }
                continue;
            }
            result = scheduler.join_next(), if !scheduler.is_empty() => {
                let (retry, result) = result.context("Slurm status task disappeared")??;
                accounting = Some(retry);
                match result {
                    Ok(Some(state)) => state,
                    Ok(None) => continue,
                    Err(error) if error.downcast_ref::<SlurmCommandTimeout>().is_some() => {
                        eprintln!("warning: cannot query Slurm job {execution_id}: {error:#}");
                        continue;
                    }
                    Err(error) => return Err(error),
                }
            }
            _ = poll.tick() => {
                cancel_due |= cancellation_requested || workspace_error.is_some();
                if let Some(mut retry) = accounting.take() {
                    let id = execution_id.to_owned();
                    scheduler.spawn(async move {
                        let state = SlurmCli::status(&id, &mut retry).await;
                        (retry, state)
                    });
                }
                continue;
            }
        };
        // Never clean a workspace until Slurm confirms that its writers stopped.
        if !matches!(
            state,
            SchedulerState::Submitted | SchedulerState::Running | SchedulerState::Finishing
        ) {
            if let Err(error) = checks.finish().await {
                workspace_error
                    .get_or_insert_with(|| format!("workspace limit violation: {error:#}"));
            }
            cancellation_requested = cancellation_requested
                || status.borrow().cancellations.contains(&assignment.job_id);
            if let Some(message) = &workspace_error {
                control
                    .fail(&config.session_id, assignment.job_id, message)
                    .await?;
                waiting.store(false, Ordering::Release);
                return Ok(());
            }
        }
        match state {
            SchedulerState::Submitted | SchedulerState::Running | SchedulerState::Finishing => {
                let phase = match state {
                    SchedulerState::Submitted => JobState::Submitted,
                    SchedulerState::Running => JobState::Running,
                    _ => JobState::Finishing,
                };
                progress
                    .report(
                        control,
                        config,
                        assignment.job_id,
                        phase,
                        execution_id,
                        waiting,
                    )
                    .await?;
            }
            SchedulerState::Cancelled => {
                if cancellation_requested {
                    let final_state = control
                        .cancel_job(&config.session_id, assignment.job_id)
                        .await?;
                    eprintln!(
                        "job {} reached Control Plane state {final_state:?}",
                        assignment.job_id
                    );
                    return Ok(());
                }
                control
                    .fail(
                        &config.session_id,
                        assignment.job_id,
                        "Slurm job was cancelled without a Control Plane cancellation request",
                    )
                    .await?;
                return Ok(());
            }
            SchedulerState::InfrastructureFailure => {
                control
                    .fail(
                        &config.session_id,
                        assignment.job_id,
                        "Slurm reported an infrastructure failure",
                    )
                    .await?;
                return Ok(());
            }
            SchedulerState::Timeout
            | SchedulerState::OutOfMemory
            | SchedulerState::Completed
            | SchedulerState::Failed => {
                let terminal = match state {
                    SchedulerState::Timeout => ExecutionTerminalState::Timeout,
                    SchedulerState::OutOfMemory => ExecutionTerminalState::OutOfMemory,
                    SchedulerState::Completed => ExecutionTerminalState::Completed,
                    _ => ExecutionTerminalState::Failed,
                };
                return finish_execution(
                    config,
                    control,
                    assignment.job_id,
                    work,
                    execution_id,
                    terminal,
                    waiting,
                    &mut progress,
                )
                .await;
            }
        }
    }
}

async fn cleanup_workspace(work: &Path) {
    if let Err(error) = tokio::fs::remove_dir_all(work).await {
        if error.kind() != std::io::ErrorKind::NotFound {
            eprintln!(
                "warning: cannot clean workspace {}: {error}",
                work.display()
            );
        }
    }
}

async fn process_assignment(
    config: &Config,
    control: &mut Control,
    assignment: JobAssignment,
    status: watch::Receiver<AgentStatus>,
    waiting: Arc<AtomicBool>,
) -> Result<()> {
    let (work, manifest) = match stage_assignment(config, &assignment, status.clone()).await {
        Ok(staged) => staged,
        Err(error)
            if error.downcast_ref::<PreparationCancelled>().is_some()
                || status.borrow().cancellations.contains(&assignment.job_id) =>
        {
            control
                .cancel_job(&config.session_id, assignment.job_id)
                .await?;
            waiting.store(false, Ordering::Release);
            return Ok(());
        }
        Err(error) => {
            eprintln!(
                "Job {} cannot stage assignment: {error:#}",
                assignment.job_id
            );
            control
                .fail(
                    &config.session_id,
                    assignment.job_id,
                    &format!("cannot stage assignment: {error:#}"),
                )
                .await?;
            waiting.store(false, Ordering::Release);
            return Ok(());
        }
    };
    if status.borrow().cancellations.contains(&assignment.job_id) {
        control
            .cancel_job(&config.session_id, assignment.job_id)
            .await?;
        waiting.store(false, Ordering::Release);
        cleanup_workspace(&work).await;
        return Ok(());
    }
    if config.executor == Executor::Docker {
        let manifest = manifest
            .docker
            .as_ref()
            .context("Docker assignment has no Docker manifest")?;
        docker::execute(
            config,
            control,
            &assignment,
            &work,
            manifest,
            status,
            &waiting,
        )
        .await?;
        waiting.store(false, Ordering::Release);
        cleanup_workspace(&work).await;
        return Ok(());
    }
    let execution_id = match SlurmCli::submit(&assignment.assigned_partition, &work).await {
        Ok(id) => id,
        Err(error) if error.downcast_ref::<UnknownSubmittedJob>().is_some() => {
            // Slurm may still be using the workspace. Retire the Agent rather
            // than deleting those files or treating this as a rejected submit.
            return Err(error);
        }
        Err(error) => {
            eprintln!(
                "Job {} cannot submit to Slurm: {error:#}",
                assignment.job_id
            );
            control
                .fail(
                    &config.session_id,
                    assignment.job_id,
                    &format!("cannot submit Slurm job: {error:#}"),
                )
                .await?;
            waiting.store(false, Ordering::Release);
            cleanup_workspace(&work).await;
            return Ok(());
        }
    };
    // Do not catch this error: after successful sbatch, an Agent that cannot persist
    // the scheduler ID must go offline so Control Plane can mark the Job failed.
    let submitted_state = control
        .report(
            &config.session_id,
            assignment.job_id,
            JobState::Submitted,
            Some(&execution_id),
        )
        .await?;
    monitor_job(
        config,
        control,
        &assignment,
        &work,
        &execution_id,
        status,
        &waiting,
        JobProgress {
            state: submitted_state,
            execution_id_recorded: true,
        },
    )
    .await?;
    waiting.store(false, Ordering::Release);
    cleanup_workspace(&work).await;
    Ok(())
}

async fn run(mut config: Config) -> Result<()> {
    if config.control_plane_http_url.scheme() == "http" {
        eprintln!("WARNING: Judge Agent is using unencrypted Artifact HTTP; use only on a trusted isolated network");
    }
    eprintln!(
        "Judge Agent Artifact endpoint: {}",
        config.control_plane_http_url
    );
    tokio::fs::create_dir_all(&config.work_root).await?;
    if config.executor == Executor::Docker {
        config.work_root = tokio::fs::canonicalize(&config.work_root).await?;
        docker::prepare(&config).await?;
    }
    let mut control = Control::connect(&config).await?;
    let heartbeat_interval = control.register(&config).await?;
    eprintln!(
        "registered Judge Agent {} for partition {}",
        config.session_id, config.partition
    );
    let config = Arc::new(config);
    let (status_tx, status_rx) = watch::channel(AgentStatus {
        state: AgentState::Online,
        cancellations: Arc::new(HashSet::new()),
    });
    // Heartbeats must not wait for staging, Slurm commands, uploads or Job RPCs.
    // A separate client shares the transport but not the mutation lock.
    let mut heartbeats = JoinSet::new();
    let heartbeat_control = control.clone();
    let heartbeat_config = Arc::clone(&config);
    heartbeats.spawn(heartbeat_loop(heartbeat_interval, status_tx, move || {
        let mut control = heartbeat_control.clone();
        let config = Arc::clone(&heartbeat_config);
        async move {
            control
                .heartbeat(&config.session_id, &config.partition, config.executor)
                .await
        }
    }));
    let mut jobs = JobTasks {
        serial: config.executor == Executor::Docker,
        ..JobTasks::default()
    };
    let mut poll = interval(config.idle_poll);
    poll.set_missed_tick_behavior(MissedTickBehavior::Delay);
    let mut last_idle_diagnostic = Instant::now() - IDLE_DIAGNOSTIC_INTERVAL;
    let stopping = Arc::new(AtomicBool::new(false));
    let signal_stopping = Arc::clone(&stopping);
    let mut signals = JoinSet::new();
    signals.spawn(async move {
        if let Err(error) = wait_for_shutdown().await {
            eprintln!("warning: shutdown signal handler failed: {error:#}");
            return;
        }
        eprintln!("received shutdown signal; stopping new claims and waiting for active Jobs");
        signal_stopping.store(true, Ordering::Release);
    });
    loop {
        let state = status_rx.borrow().state;
        if (stopping.load(Ordering::Acquire) || state == AgentState::Ended) && jobs.is_empty() {
            return Ok(());
        }
        tokio::select! {
            result = heartbeats.join_next(), if !heartbeats.is_empty() => {
                let outcome = result.context("heartbeat task disappeared")?;
                outcome??;
            }
            result = jobs.join_next(), if !jobs.is_empty() => {
                result?;
            }
            _ = poll.tick() => {
                if jobs.can_claim(stopping.load(Ordering::Acquire), status_rx.borrow().state) {
                    let claim = match control.claim(&config.session_id).await {
                        Ok(claim) => claim,
                        // Admin drain can race the preceding heartbeat. The next
                        // heartbeat supplies the authoritative lifecycle state.
                        Err(error) if error.downcast_ref::<tonic::Status>().is_some_and(|status| status.code() == tonic::Code::FailedPrecondition) => continue,
                        Err(error) => return Err(error),
                    };
                    if let Some(assignment) = claim.assignment {
                        eprintln!(
                            "claimed Job {} for submission {} on partition {}",
                            assignment.job_id, assignment.submission_id, assignment.assigned_partition
                        );
                        let id = assignment.job_id;
                        let config = Arc::clone(&config);
                        let mut control = control.clone();
                        let status = status_rx.clone();
                        jobs.spawn(id, move |waiting| async move {
                            process_assignment(&config, &mut control, assignment, status, waiting).await
                        })?;
                    } else if last_idle_diagnostic.elapsed() >= IDLE_DIAGNOSTIC_INTERVAL {
                        last_idle_diagnostic = Instant::now();
                        if claim.pending_job_count == 0 {
                            eprintln!("no pending Jobs");
                        } else if claim.eligible_pending_job_count == 0 {
                            eprintln!(
                                "no pending Job eligible for partition {} with executor {:?}; {} pending Job(s) use another partition or executor",
                                config.partition, config.executor, claim.pending_job_count
                            );
                        } else {
                            eprintln!(
                                "no Job claimed for partition {}; {}/{} pending Job(s) are eligible and may be held by another Agent",
                                config.partition,
                                claim.eligible_pending_job_count,
                                claim.pending_job_count
                            );
                        }
                    }
                }
            }
        }
    }
}

async fn heartbeat_loop<F, Fut>(
    period: Duration,
    sender: watch::Sender<AgentStatus>,
    mut probe: F,
) -> Result<()>
where
    F: FnMut() -> Fut,
    Fut: Future<Output = Result<AgentStatus>>,
{
    let mut timer = interval(period);
    timer.set_missed_tick_behavior(MissedTickBehavior::Delay);
    loop {
        timer.tick().await;
        let status = probe().await?;
        let ended = status.state == AgentState::Ended;
        if sender.send(status).is_err() || ended {
            return Ok(());
        }
    }
}

#[derive(Default)]
struct JobTasks {
    tasks: JoinSet<(i64, Result<()>)>,
    waiting: HashMap<i64, Arc<AtomicBool>>,
    serial: bool,
}

impl JobTasks {
    fn is_empty(&self) -> bool {
        self.tasks.is_empty()
    }

    fn can_claim(&self, stopping: bool, state: AgentState) -> bool {
        !stopping
            && state == AgentState::Online
            && (!self.serial || self.is_empty())
            && !self
                .waiting
                .values()
                .any(|waiting| waiting.load(Ordering::Acquire))
    }

    fn spawn<F, Fut>(&mut self, id: i64, worker: F) -> Result<()>
    where
        F: FnOnce(Arc<AtomicBool>) -> Fut,
        Fut: Future<Output = Result<()>> + Send + 'static,
    {
        if self.waiting.contains_key(&id) {
            bail!("Control Plane replayed locally active Job {id}");
        }
        let waiting = Arc::new(AtomicBool::new(true));
        self.waiting.insert(id, Arc::clone(&waiting));
        let future = worker(waiting);
        self.tasks.spawn(async move { (id, future.await) });
        Ok(())
    }

    async fn join_next(&mut self) -> Result<()> {
        let (id, result) = self
            .tasks
            .join_next()
            .await
            .context("job task disappeared")??;
        self.waiting.remove(&id);
        result
    }
}

#[tokio::main]
async fn main() {
    let mut args = std::env::args_os();
    let program = args.next().unwrap_or_default();
    let config_path = args.next().unwrap_or_else(|| "judge.toml".into());
    if args.next().is_some() {
        eprintln!("usage: {} [config-file]", PathBuf::from(program).display());
        std::process::exit(2);
    }
    match Config::from_file(Path::new(&config_path)) {
        Ok(config) => {
            if let Err(error) = run(config).await {
                eprintln!("Judge Agent stopped: {error:#}");
                std::process::exit(1);
            }
        }
        Err(error) => {
            eprintln!("Judge Agent configuration error: {error:#}");
            std::process::exit(2);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{
        decimal, loopback, parse_scheduler_json, workspace_within_limit, Config, SchedulerState,
        WorkspaceScan, MAX_WORKSPACE_BYTES,
    };
    use std::{
        fs,
        path::{Path, PathBuf},
        time::{SystemTime, UNIX_EPOCH},
    };

    #[test]
    fn accepted_submission_with_unknown_id_must_retain_workspace() {
        use super::{submitted_job_id, UnknownSubmittedJob};
        assert_eq!(submitted_job_id(b"123;cluster\n").unwrap(), "123");
        for stdout in [b"".as_slice(), b"not-an-id", &[0xff]] {
            assert!(submitted_job_id(stdout)
                .unwrap_err()
                .downcast_ref::<UnknownSubmittedJob>()
                .is_some());
        }
    }

    #[tokio::test(start_paused = true)]
    async fn concurrent_jobs_keep_heartbeats_and_only_block_claims_while_waiting() {
        use super::*;
        use tokio::sync::oneshot;
        let mut jobs = JobTasks::default();
        let (tx, mut rx) = watch::channel(AgentStatus {
            state: AgentState::Online,
            cancellations: Arc::new(HashSet::new()),
        });
        let mut heartbeats = JoinSet::new();
        heartbeats.spawn(heartbeat_loop(Duration::from_secs(5), tx, || async {
            Ok(AgentStatus {
                state: AgentState::Online,
                cancellations: Arc::new(HashSet::from([1])),
            })
        }));
        let (running_tx, running_rx) = oneshot::channel();
        let (finish_tx, finish_rx) = oneshot::channel();
        let (released_tx, released_rx) = oneshot::channel();
        jobs.spawn(1, |waiting| async move {
            running_rx.await?;
            waiting.store(false, Ordering::Release);
            released_tx.send(()).unwrap();
            finish_rx.await?;
            Ok(())
        })
        .unwrap();
        assert!(!jobs.can_claim(false, AgentState::Online));
        // Staging can take longer than the Control Plane's 30-second offline
        // threshold without blocking heartbeat delivery or cancellation intent.
        for _ in 0..8 {
            rx.changed().await.unwrap();
            assert_eq!(*rx.borrow_and_update().cancellations, HashSet::from([1]));
            tokio::time::advance(Duration::from_secs(5)).await;
        }
        assert!(!jobs.can_claim(false, AgentState::Online));
        running_tx.send(()).unwrap();
        released_rx.await.unwrap();
        assert!(jobs.can_claim(false, AgentState::Online));
        assert!(!jobs.is_empty());
        assert!(!jobs.can_claim(true, AgentState::Online));
        assert!(!jobs.can_claim(false, AgentState::Draining));
        assert!(!jobs.can_claim(false, AgentState::Ended));
        assert!(jobs.spawn(1, |_| async { Ok(()) }).is_err());

        let (second_tx, second_rx) = oneshot::channel();
        jobs.spawn(2, |_| async move {
            second_rx.await?;
            Ok(())
        })
        .unwrap();
        assert!(!jobs.can_claim(false, AgentState::Online));
        // Completing a running Job must not release another Job's queue slot.
        finish_tx.send(()).unwrap();
        jobs.join_next().await.unwrap();
        assert!(!jobs.can_claim(false, AgentState::Online));
        second_tx.send(()).unwrap();
        jobs.join_next().await.unwrap();
        assert!(jobs.can_claim(false, AgentState::Online));
        assert!(jobs.is_empty());
    }

    #[tokio::test(start_paused = true)]
    async fn heartbeat_delivers_drain_and_stops_normally_on_ended() {
        use super::*;
        let (tx, mut rx) = watch::channel(AgentStatus {
            state: AgentState::Online,
            cancellations: Arc::new(HashSet::new()),
        });
        let task = tokio::spawn(heartbeat_loop(Duration::from_millis(1), tx, {
            let mut states = [AgentState::Draining, AgentState::Ended].into_iter();
            move || {
                let state = states.next().unwrap();
                async move {
                    Ok(AgentStatus {
                        state,
                        cancellations: Arc::new(HashSet::from([5])),
                    })
                }
            }
        }));
        rx.changed().await.unwrap();
        assert_eq!(rx.borrow_and_update().state, AgentState::Draining);
        task.await.unwrap().unwrap();
        assert_eq!(rx.borrow().state, AgentState::Ended);
    }

    #[tokio::test]
    async fn heartbeat_and_worker_failures_are_observable() {
        use super::*;
        let (tx, _rx) = watch::channel(AgentStatus {
            state: AgentState::Online,
            cancellations: Arc::new(HashSet::new()),
        });
        assert!(heartbeat_loop(Duration::from_secs(5), tx, || async {
            anyhow::bail!("heartbeat unavailable")
        })
        .await
        .is_err());
        let mut jobs = JobTasks::default();
        jobs.spawn(1, |_| async { anyhow::bail!("job unavailable") })
            .unwrap();
        assert!(jobs.join_next().await.is_err());
        assert!(jobs.is_empty());
    }

    #[tokio::test]
    async fn cleanup_removes_only_the_finished_job_workspace() {
        use super::*;
        let root = std::env::temp_dir().join(format!("soj-cleanup-test-{}", Uuid::new_v4()));
        let finished = root.join("1");
        let active = root.join("2");
        tokio::fs::create_dir_all(finished.join("output"))
            .await
            .unwrap();
        tokio::fs::create_dir_all(&active).await.unwrap();
        tokio::fs::write(finished.join("output/result.json"), b"result")
            .await
            .unwrap();
        tokio::fs::write(active.join("source"), b"keep")
            .await
            .unwrap();
        cleanup_workspace(&finished).await;
        cleanup_workspace(&finished).await;
        assert!(!finished.exists());
        assert_eq!(
            tokio::fs::read(active.join("source")).await.unwrap(),
            b"keep"
        );
        tokio::fs::remove_dir_all(root).await.unwrap();
    }

    fn config_file(contents: &str) -> PathBuf {
        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let path = std::env::temp_dir().join(format!("soj-judge-config-test-{unique}.toml"));
        fs::write(&path, contents).unwrap();
        path
    }

    #[test]
    fn reads_tls_and_http_settings_from_toml() {
        let path = config_file(
            r#"
agent_session_id = "11111111-1111-4111-8111-111111111111"
agent_partition = "cpu"
agent_service_token = "test-token"
judge_work_root = "/var/lib/soj-judge/work"
grpc_addr = "control-plane.internal:50051"
grpc_server_name = "control-plane.internal"
grpc_ca_file = "/etc/soj/control-plane-ca.pem"
control_plane_http_url = "http://control-plane.internal:8080"
judge_idle_poll_ms = 250
"#,
        );
        let config = Config::from_file(&path).unwrap();
        fs::remove_file(path).unwrap();

        assert_eq!(config.grpc_endpoint, "https://control-plane.internal:50051");
        assert_eq!(
            config.grpc_server_name.as_deref(),
            Some("control-plane.internal")
        );
        assert_eq!(config.control_plane_http_url.scheme(), "http");
        assert_eq!(config.idle_poll.as_millis(), 250);
        assert_eq!(config.executor, super::Executor::Slurm);
        assert_eq!(config.docker_socket, PathBuf::from("/var/run/docker.sock"));
    }

    #[test]
    fn selects_executor_and_rejects_remote_docker_configuration() {
        for (executor, socket, work, valid) in [
            ("docker", "/var/run/docker.sock", "/var/lib/soj/work", true),
            ("slurm", "/var/run/docker.sock", "/var/lib/soj/work", true),
            (
                "unknown",
                "/var/run/docker.sock",
                "/var/lib/soj/work",
                false,
            ),
            ("docker", "tcp://daemon:2375", "/var/lib/soj/work", false),
            ("docker", "docker.sock", "/var/lib/soj/work", false),
            (
                "docker",
                "/var/run/docker.sock",
                "/var/lib/soj/work,readonly",
                false,
            ),
        ] {
            let path = config_file(&format!(
                r#"
agent_executor = "{executor}"
docker_socket = "{socket}"
agent_partition = "docker"
agent_service_token = "test-token"
judge_work_root = "{work}"
grpc_addr = "localhost:50051"
grpc_insecure_dev = true
control_plane_http_url = "http://localhost:8080"
"#
            ));
            let parsed = Config::from_file(&path);
            fs::remove_file(path).unwrap();
            assert_eq!(
                parsed.is_ok(),
                valid,
                "executor={executor}, socket={socket}"
            );
            if executor == "docker" && valid {
                assert_eq!(parsed.unwrap().executor, super::Executor::Docker);
            }
        }
    }

    #[test]
    fn rejects_invalid_or_unknown_toml_settings() {
        let missing_ca = config_file(
            r#"
agent_partition = "cpu"
agent_service_token = "test-token"
judge_work_root = "/var/lib/soj-judge/work"
grpc_addr = "control-plane.internal:50051"
control_plane_http_url = "https://control-plane.internal:8080"
"#,
        );
        let error = Config::from_file(&missing_ca).err().unwrap();
        assert!(error.to_string().contains("grpc_ca_file is required"));
        fs::remove_file(missing_ca).unwrap();

        let unknown = config_file(
            r#"
agent_partition = "cpu"
agent_service_token = "test-token"
judge_work_root = "/var/lib/soj-judge/work"
grpc_addr = "control-plane.internal:50051"
grpc_ca_file = "/etc/soj/control-plane-ca.pem"
control_plane_http_url = "https://control-plane.internal:8080"
unrecognized_setting = true
"#,
        );
        assert!(Config::from_file(&unknown).is_err());
        fs::remove_file(unknown).unwrap();
    }

    #[test]
    fn legacy_insecure_mode_only_accepts_loopback() {
        assert!(loopback("localhost:50051"));
        assert!(loopback("[::1]:50051"));
        assert!(!loopback("0.0.0.0:50051"));
    }

    #[test]
    fn parses_supported_slurm_json_shapes() {
        assert_eq!(
            parse_scheduler_json(br#"{"jobs":[{"job_state":"RUNNING"}]}"#).unwrap(),
            Some(SchedulerState::Running)
        );
        assert_eq!(
            parse_scheduler_json(br#"{"jobs":[{"job_state":["COMPLETING"]}]}"#).unwrap(),
            Some(SchedulerState::Finishing)
        );
        assert_eq!(
            parse_scheduler_json(br#"{"jobs":[{"state":{"current":["OUT_OF_MEMORY"]}}]}"#).unwrap(),
            Some(SchedulerState::OutOfMemory)
        );
        assert_eq!(parse_scheduler_json(br#"{"jobs":[]}"#).unwrap(), None);
    }

    #[test]
    fn terminal_slurm_states_wait_for_completing_to_clear() {
        for (base, terminal) in [
            ("COMPLETED", SchedulerState::Completed),
            ("FAILED", SchedulerState::Failed),
            ("CANCELLED", SchedulerState::Cancelled),
            ("TIMEOUT", SchedulerState::Timeout),
            ("OUT_OF_MEMORY", SchedulerState::OutOfMemory),
            ("NODE_FAIL", SchedulerState::InfrastructureFailure),
        ] {
            let finishing = serde_json::json!({"jobs": [{"job_state": [base, "COMPLETING"]}]});
            assert_eq!(
                parse_scheduler_json(&serde_json::to_vec(&finishing).unwrap()).unwrap(),
                Some(SchedulerState::Finishing),
                "{base} must not terminate while cleanup is running"
            );
            let stopped = serde_json::json!({"jobs": [{"job_state": [base]}]});
            assert_eq!(
                parse_scheduler_json(&serde_json::to_vec(&stopped).unwrap()).unwrap(),
                Some(terminal),
                "{base} must become terminal after cleanup finishes"
            );
        }
    }

    #[test]
    fn completing_takes_precedence_in_all_slurm_state_representations() {
        for input in [
            br#"{"jobs":[{"job_state":["COMPLETING","CANCELLED"]}]}"#.as_slice(),
            br#"{"jobs":[{"job_state":["COMPLETED","UPDATE_DB","COMPLETING"]}]}"#,
            br#"{"jobs":[{"state":{"current":["COMPLETED","COMPLETING"]}}]}"#,
            br#"{"jobs":[{"job_state":"COMPLETED+COMPLETING"}]}"#,
            br#"{"jobs":[{"state":{"current":"COMPLETING CANCELLED"}}]}"#,
            br#"{"jobs":[{"job_state":["RUNNING","COMPLETING"]}]}"#,
        ] {
            assert_eq!(
                parse_scheduler_json(input).unwrap(),
                Some(SchedulerState::Finishing)
            );
        }
    }

    #[test]
    fn validates_decimal_lexemes_without_float_conversion() {
        assert!(decimal("0"));
        assert!(decimal("815.4"));
        assert!(!decimal("01"));
        assert!(!decimal("1."));
        assert!(!decimal("-1"));
    }

    #[test]
    fn rejects_workspace_larger_than_limit() {
        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let work = std::env::temp_dir().join(format!("soj-judge-workspace-test-{unique}"));
        fs::create_dir(&work).unwrap();
        let file = fs::File::create(work.join("large")).unwrap();
        file.set_len(MAX_WORKSPACE_BYTES + 1).unwrap();
        for scan in [WorkspaceScan::Active, WorkspaceScan::Final] {
            assert!(workspace_within_limit(&work, scan).is_err());
        }
        fs::remove_dir_all(&work).unwrap();
    }
    fn temporary_workspace(label: &str) -> PathBuf {
        let path = std::env::temp_dir().join(format!("soj-{label}-{}", super::Uuid::new_v4()));
        fs::create_dir(&path).unwrap();
        path
    }

    #[cfg(unix)]
    #[test]
    fn workspace_counts_directories_and_links_without_following_them() {
        use super::MAX_WORKSPACE_ENTRIES;
        use std::os::unix::fs::symlink;
        let work = temporary_workspace("workspace-entries");
        for index in 0..MAX_WORKSPACE_ENTRIES / 2 {
            fs::create_dir(work.join(format!("directory-{index}"))).unwrap();
            symlink("/does-not-exist", work.join(format!("link-{index}"))).unwrap();
        }
        workspace_within_limit(&work, WorkspaceScan::Final).unwrap();
        fs::write(work.join("one-too-many"), []).unwrap();
        assert!(workspace_within_limit(&work, WorkspaceScan::Active)
            .unwrap_err()
            .to_string()
            .contains("entry"));
        fs::remove_dir_all(work).unwrap();
    }

    #[test]
    fn workspace_depth_boundary_applies_to_every_entry() {
        use super::MAX_WORKSPACE_DEPTH;
        let work = temporary_workspace("workspace-depth");
        let mut deepest = work.clone();
        for _ in 0..MAX_WORKSPACE_DEPTH {
            deepest.push("d");
            fs::create_dir(&deepest).unwrap();
        }
        workspace_within_limit(&work, WorkspaceScan::Final).unwrap();
        fs::write(deepest.join("too-deep"), []).unwrap();
        assert!(workspace_within_limit(&work, WorkspaceScan::Active)
            .unwrap_err()
            .to_string()
            .contains("depth"));
        fs::remove_dir_all(work).unwrap();
    }

    fn package_archive(root: &Path, paths: &[PathBuf], kind: tar::EntryType) -> PathBuf {
        let archive = root.join(format!("package-{}.tar.zst", super::Uuid::new_v4()));
        let encoder =
            zstd::stream::write::Encoder::new(fs::File::create(&archive).unwrap(), 1).unwrap();
        let mut builder = tar::Builder::new(encoder);
        for path in paths {
            let mut header = tar::Header::new_gnu();
            header.set_entry_type(kind);
            header.set_mode(0o755);
            header.set_size(0);
            if kind.is_symlink() {
                header.set_link_name("outside").unwrap();
            }
            builder
                .append_data(&mut header, path, std::io::empty())
                .unwrap();
        }
        builder.into_inner().unwrap().finish().unwrap();
        archive
    }

    #[tokio::test]
    async fn package_counts_directory_entries_even_when_the_same_directory_repeats() {
        use super::{unpack_package, MAX_PACKAGE_ENTRIES};
        let root = temporary_workspace("package-entries");
        let package = root.join("package");
        fs::create_dir(&package).unwrap();
        let mut paths = vec![PathBuf::from("directory"); MAX_PACKAGE_ENTRIES as usize];
        let archive = package_archive(&root, &paths, tar::EntryType::Directory);
        unpack_package(archive, package.clone(), Default::default())
            .await
            .unwrap();
        paths.push(PathBuf::from("directory"));
        let archive = package_archive(&root, &paths, tar::EntryType::Directory);
        let error = unpack_package(archive, package, Default::default())
            .await
            .unwrap_err();
        assert!(error.to_string().contains("too many entries"));
        fs::remove_dir_all(root).unwrap();
    }

    #[tokio::test]
    async fn package_rejects_links_and_excessive_implicit_parents() {
        use super::{unpack_package, MAX_WORKSPACE_DEPTH, MAX_WORKSPACE_ENTRIES};
        let root = temporary_workspace("package-parents");
        let package = root.join("package");
        fs::create_dir(&package).unwrap();
        let archive = package_archive(&root, &[PathBuf::from("link")], tar::EntryType::Symlink);
        assert!(unpack_package(archive, package.clone(), Default::default())
            .await
            .unwrap_err()
            .to_string()
            .contains("link"));
        assert!(!package.join("link").exists());
        let depth = MAX_WORKSPACE_DEPTH - 1;
        let count = MAX_WORKSPACE_ENTRIES as usize / depth + 1;
        let paths: Vec<_> = (0..count)
            .map(|index| {
                let mut path = PathBuf::from(format!("root-{index}"));
                for _ in 1..depth {
                    path.push("d");
                }
                path
            })
            .collect();
        let archive = package_archive(&root, &paths, tar::EntryType::Regular);
        let error = unpack_package(archive, package, Default::default())
            .await
            .unwrap_err();
        assert!(error.to_string().contains("workspace entries"));
        fs::remove_dir_all(root).unwrap();
    }

    #[tokio::test]
    async fn package_depth_includes_the_package_directory() {
        use super::{unpack_package, MAX_WORKSPACE_DEPTH};
        let root = temporary_workspace("package-depth");
        let package = root.join("package");
        fs::create_dir(&package).unwrap();
        let mut path = PathBuf::new();
        for _ in 1..MAX_WORKSPACE_DEPTH {
            path.push("d");
        }
        let archive = package_archive(&root, &[path.clone()], tar::EntryType::Directory);
        unpack_package(archive, package.clone(), Default::default())
            .await
            .unwrap();
        path.push("too-deep");
        let archive = package_archive(&root, &[path], tar::EntryType::Regular);
        assert!(unpack_package(archive, package, Default::default())
            .await
            .unwrap_err()
            .to_string()
            .contains("depth"));
        fs::remove_dir_all(root).unwrap();
    }

    #[tokio::test(start_paused = true)]
    async fn blocked_workspace_scan_does_not_delay_cancellation_or_overlap_another_scan() {
        use super::*;
        use tokio::sync::{mpsc, Notify};
        let work = temporary_workspace("scan-cancellation");
        let (stop, stopping) = watch::channel(false);
        let (errors, _error_receiver) = watch::channel(None);
        let (started, mut starts) = mpsc::unbounded_channel();
        let gate = Arc::new(Notify::new());
        let active = Arc::new(std::sync::atomic::AtomicUsize::new(0));
        let scanner = tokio::spawn(workspace_check_loop(stopping, errors, {
            let work = work.clone();
            let gate = Arc::clone(&gate);
            let active = Arc::clone(&active);
            move || {
                let work = work.clone();
                let gate = Arc::clone(&gate);
                let active = Arc::clone(&active);
                let started = started.clone();
                async move {
                    assert_eq!(active.fetch_add(1, Ordering::AcqRel), 0);
                    started.send(()).unwrap();
                    gate.notified().await;
                    let outcome = workspace_within_limit(&work, WorkspaceScan::Active);
                    active.fetch_sub(1, Ordering::AcqRel);
                    outcome
                }
            }
        }));
        starts.recv().await.unwrap();
        tokio::time::advance(WORKSPACE_CHECK_INTERVAL * 6).await;
        assert!(starts.try_recv().is_err());
        let (status_tx, status_rx) = watch::channel(AgentStatus {
            state: AgentState::Online,
            cancellations: Arc::new(HashSet::new()),
        });
        status_tx
            .send(AgentStatus {
                state: AgentState::Online,
                cancellations: Arc::new(HashSet::from([77])),
            })
            .unwrap();
        timeout(
            Duration::from_millis(1),
            wait_for_cancellation(status_rx, 77),
        )
        .await
        .unwrap();
        assert_eq!(active.load(Ordering::Acquire), 1);
        // Shutdown must still wait for the existing scan, not abandon it.
        stop.send(true).unwrap();
        tokio::task::yield_now().await;
        assert!(!scanner.is_finished());
        gate.notify_one();
        scanner.await.unwrap().unwrap();
        assert_eq!(active.load(Ordering::Acquire), 0);
        assert!(starts.try_recv().is_err());
        fs::remove_dir_all(work).unwrap();
    }

    #[tokio::test(start_paused = true)]
    async fn workspace_checks_continue_after_success_and_after_a_violation() {
        use super::*;
        use tokio::sync::mpsc;
        let work = temporary_workspace("scan-period");
        let (stop, stopping) = watch::channel(false);
        let (errors, mut error_rx) = watch::channel(None);
        let (finished, mut scans) = mpsc::unbounded_channel();
        let scanner = tokio::spawn(workspace_check_loop(stopping, errors, {
            let work = work.clone();
            move || {
                let result = workspace_within_limit(&work, WorkspaceScan::Active);
                finished.send(()).unwrap();
                std::future::ready(result)
            }
        }));
        scans.recv().await.unwrap();
        let large = work.join("large");
        fs::File::create(&large)
            .unwrap()
            .set_len(MAX_WORKSPACE_BYTES + 1)
            .unwrap();
        tokio::time::advance(WORKSPACE_CHECK_INTERVAL - Duration::from_millis(1)).await;
        assert!(scans.try_recv().is_err());
        tokio::time::advance(Duration::from_millis(1)).await;
        scans.recv().await.unwrap();
        error_rx.changed().await.unwrap();
        assert!(error_rx.borrow().as_ref().unwrap().contains("byte limit"));
        fs::remove_file(large).unwrap();
        tokio::time::advance(WORKSPACE_CHECK_INTERVAL).await;
        scans.recv().await.unwrap();
        stop.send(true).unwrap();
        scanner.await.unwrap().unwrap();
        fs::remove_dir_all(work).unwrap();
    }

    #[tokio::test]
    async fn terminal_workspace_check_finishes_before_cleanup_and_checks_the_final_tree() {
        use super::*;
        let work = temporary_workspace("terminal-scan");
        let mut checks = WorkspaceChecks::new(&work);
        fs::File::create(work.join("late-output"))
            .unwrap()
            .set_len(MAX_WORKSPACE_BYTES + 1)
            .unwrap();
        assert!(checks.finish().await.is_err());
        assert!(work.exists());
        cleanup_workspace(&work).await;
        assert!(!work.exists());
    }

    #[test]
    fn repeated_and_regressing_scheduler_observations_do_not_repeat_reports() {
        use super::{JobProgress, JobState};
        let mut progress = JobProgress {
            state: JobState::Claimed,
            execution_id_recorded: false,
        };
        let observations = [
            JobState::Submitted,
            JobState::Submitted,
            JobState::Running,
            JobState::Running,
            JobState::Submitted,
            JobState::Finishing,
            JobState::Running,
            JobState::Finishing,
            JobState::Finishing,
        ];
        let mut reports = Vec::new();
        for state in observations {
            if progress.needs_report(state, true) {
                reports.push(state);
                progress.acknowledge(state, state, true);
            }
        }
        assert_eq!(
            reports,
            [JobState::Submitted, JobState::Running, JobState::Finishing]
        );
        assert_eq!(progress.state, JobState::Finishing);
        let mut missing_id = JobProgress {
            state: JobState::Running,
            execution_id_recorded: false,
        };
        assert!(missing_id.needs_report(JobState::Running, true));
        missing_id.acknowledge(JobState::Running, JobState::Finishing, true);
        assert!(!missing_id.needs_report(JobState::Running, true));
        assert!(!missing_id.needs_report(JobState::Finishing, true));
    }

    #[tokio::test(start_paused = true)]
    async fn missing_accounting_uses_bounded_exponential_backoff_and_resets_on_observation() {
        use super::*;
        let mut retry = AccountingRetry::new();
        assert!(retry.due());
        for seconds in [1, 2, 4, 8, 16, 30, 30] {
            retry.observe(None);
            assert!(!retry.due());
            tokio::time::advance(Duration::from_secs(seconds) - Duration::from_millis(1)).await;
            assert!(!retry.due());
            tokio::time::advance(Duration::from_millis(1)).await;
            assert!(retry.due());
        }
        retry.observe(Some(SchedulerState::Running));
        assert!(retry.due());
        retry.observe(None);
        tokio::time::advance(ACCOUNTING_RETRY_INITIAL).await;
        assert!(retry.due());
        assert_eq!(retry.delay, ACCOUNTING_RETRY_INITIAL * 2);
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn submit_timeout_kills_and_reaps_the_cli_and_preserves_the_workspace() {
        use super::*;
        let work = temporary_workspace("submit-timeout");
        let pid_file = work.join("cli.pid");
        let mut command = Command::new("/bin/sh");
        // This is a real blocked process, not a fake scheduler response.
        command
            .args([
                "-c",
                "printf '%s' \"$$\" > \"$1\"; exec sleep 60",
                "soj-timeout",
            ])
            .arg(&pid_file);
        let started = Instant::now();
        let error = SlurmCli::submit_command(&mut command, Duration::from_millis(250))
            .await
            .unwrap_err();
        assert!(error.downcast_ref::<UnknownSubmittedJob>().is_some());
        assert!(error.downcast_ref::<SlurmCommandTimeout>().is_some());
        assert!(started.elapsed() < Duration::from_secs(5));
        let pid: u32 = fs::read_to_string(pid_file).unwrap().parse().unwrap();
        assert!(!PathBuf::from(format!("/proc/{pid}")).exists());
        assert!(work.is_dir());
        fs::remove_dir_all(work).unwrap();
    }

    #[tokio::test]
    async fn status_cli_timeout_is_an_error_not_a_terminal_scheduler_state() {
        use super::*;
        let mut command = Command::new("sleep");
        command.arg("60");
        let error = run_slurm_command(&mut command, "squeue", Duration::from_millis(25))
            .await
            .unwrap_err();
        assert!(error.downcast_ref::<SlurmCommandTimeout>().is_some());
        assert!(error.downcast_ref::<UnknownSubmittedJob>().is_none());
    }
    #[tokio::test]
    async fn package_entry_limit_counts_directories_without_double_counting_gnu_metadata() {
        use super::{unpack_package, MAX_PACKAGE_ENTRIES};
        let root = temporary_workspace("package-metadata-count");
        let package = root.join("package");
        fs::create_dir(&package).unwrap();
        let mut paths = vec![PathBuf::from("d".repeat(200)); MAX_PACKAGE_ENTRIES as usize];
        let archive = package_archive(&root, &paths, tar::EntryType::Directory);
        unpack_package(archive, package.clone(), Default::default())
            .await
            .unwrap();
        paths.push(PathBuf::from("d".repeat(200)));
        let archive = package_archive(&root, &paths, tar::EntryType::Directory);
        assert!(unpack_package(archive, package, Default::default())
            .await
            .unwrap_err()
            .to_string()
            .contains("too many entries"));
        fs::remove_dir_all(root).unwrap();
    }

    #[tokio::test]
    async fn package_preserves_pax_size_framing_across_gnu_metadata_and_uses_the_first_size_key() {
        use super::unpack_package;
        let root = temporary_workspace("package-pax-size");
        let package = root.join("package");
        fs::create_dir(&package).unwrap();
        let archive = root.join("package.tar.zst");
        let encoder =
            zstd::stream::write::Encoder::new(fs::File::create(&archive).unwrap(), 1).unwrap();
        let mut builder = tar::Builder::new(encoder);
        builder
            .append_pax_extensions([("size", b"3".as_slice()), ("size", b"999".as_slice())])
            .unwrap();
        let long_name = "f".repeat(200);
        let mut header = tar::Header::new_gnu();
        header.set_mode(0o600);
        header.set_size(0);
        builder
            .append_data(&mut header, &long_name, b"abc".as_slice())
            .unwrap();
        header.set_size(2);
        builder
            .append_data(&mut header, "next", b"ok".as_slice())
            .unwrap();
        builder.into_inner().unwrap().finish().unwrap();
        unpack_package(archive, package.clone(), Default::default())
            .await
            .unwrap();
        assert_eq!(fs::read(package.join(long_name)).unwrap(), b"abc");
        assert_eq!(fs::read(package.join("next")).unwrap(), b"ok");
        fs::remove_dir_all(root).unwrap();
    }

    #[tokio::test]
    async fn pax_size_with_leading_zeroes_preserves_a_multi_block_file() {
        use super::unpack_package;
        let root = temporary_workspace("package-pax-leading-zeroes");
        let package = root.join("package");
        fs::create_dir(&package).unwrap();
        let archive = root.join("package.tar.zst");
        let encoder =
            zstd::stream::write::Encoder::new(fs::File::create(&archive).unwrap(), 1).unwrap();
        let mut builder = tar::Builder::new(encoder);
        let value = format!("+{}513", "0".repeat(40));
        builder
            .append_pax_extensions([("size", value.as_bytes())])
            .unwrap();
        let mut header = tar::Header::new_gnu();
        header.set_mode(0o600);
        header.set_size(0);
        let payload = vec![b'a'; 513];
        builder
            .append_data(&mut header, "payload", payload.as_slice())
            .unwrap();
        header.set_size(2);
        builder
            .append_data(&mut header, "next", b"ok".as_slice())
            .unwrap();
        builder.into_inner().unwrap().finish().unwrap();
        unpack_package(archive, package.clone(), Default::default())
            .await
            .unwrap();
        assert_eq!(fs::read(package.join("payload")).unwrap(), payload);
        assert_eq!(fs::read(package.join("next")).unwrap(), b"ok");
        fs::remove_dir_all(root).unwrap();
    }

    #[tokio::test]
    async fn package_rejects_declared_expansion_over_ten_gib_before_unpacking() {
        use super::{unpack_package, MAX_UNPACKED_PACKAGE_BYTES};
        let root = temporary_workspace("package-byte-limit");
        let package = root.join("package");
        fs::create_dir(&package).unwrap();
        let archive = root.join("package.tar.zst");
        let encoder =
            zstd::stream::write::Encoder::new(fs::File::create(&archive).unwrap(), 1).unwrap();
        let mut builder = tar::Builder::new(encoder);
        let mut header = tar::Header::new_gnu();
        header.set_path("oversized").unwrap();
        header.set_mode(0o600);
        header.set_size(MAX_UNPACKED_PACKAGE_BYTES + 1);
        header.set_cksum();
        builder.append(&header, std::io::empty()).unwrap();
        builder.into_inner().unwrap().finish().unwrap();
        assert!(unpack_package(archive, package.clone(), Default::default())
            .await
            .unwrap_err()
            .to_string()
            .contains("expands"));
        assert!(!package.join("oversized").exists());
        fs::remove_dir_all(root).unwrap();
    }
    #[tokio::test]
    async fn unsuccessful_running_report_does_not_release_waiting_or_advance_progress() {
        use super::*;
        let work = temporary_workspace("report-order");
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let address = listener.local_addr().unwrap();
        drop(listener);
        let endpoint = Endpoint::from_shared(format!("http://{address}"))
            .unwrap()
            .connect_timeout(Duration::from_millis(100))
            .timeout(Duration::from_millis(100));
        let mut control = Control {
            client: JudgeControlServiceClient::new(endpoint.connect_lazy()),
            bearer: "Bearer test".parse().unwrap(),
            mutations: Arc::new(Mutex::new(())),
        };
        let config = Config {
            session_id: Uuid::new_v4().to_string(),
            partition: "cpu".to_owned(),
            executor: Executor::Slurm,
            docker_socket: PathBuf::from("/var/run/docker.sock"),
            grpc_endpoint: format!("http://{address}"),
            grpc_server_name: None,
            insecure_grpc_dev: true,
            grpc_ca_file: None,
            token: "test".to_owned(),
            control_plane_http_url: reqwest::Url::parse("http://127.0.0.1:8080").unwrap(),
            work_root: work.clone(),
            idle_poll: Duration::from_secs(1),
        };
        let waiting = AtomicBool::new(true);
        let mut progress = JobProgress {
            state: JobState::Submitted,
            execution_id_recorded: true,
        };
        let result = progress
            .report(&mut control, &config, 1, JobState::Running, "123", &waiting)
            .await;
        assert!(result.is_err());
        assert!(waiting.load(Ordering::Acquire));
        assert_eq!(progress.state, JobState::Submitted);
        assert!(progress.needs_report(JobState::Running, true));
        fs::remove_dir_all(work).unwrap();
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn submit_distinguishes_definite_failures_from_an_interrupted_started_process() {
        use super::*;
        let mut missing = Command::new("/no-such-soj-sbatch-binary");
        let error = SlurmCli::submit_command(&mut missing, Duration::from_secs(1))
            .await
            .unwrap_err();
        assert!(error.downcast_ref::<UnknownSubmittedJob>().is_none());
        let mut rejected = Command::new("false");
        let error = SlurmCli::submit_command(&mut rejected, Duration::from_secs(1))
            .await
            .unwrap_err();
        assert!(error.downcast_ref::<UnknownSubmittedJob>().is_none());
        let mut interrupted = Command::new("/bin/sh");
        interrupted.args(["-c", "kill -TERM $$"]);
        let error = SlurmCli::submit_command(&mut interrupted, Duration::from_secs(1))
            .await
            .unwrap_err();
        assert!(error.downcast_ref::<UnknownSubmittedJob>().is_some());
    }

    #[tokio::test(start_paused = true)]
    async fn cancellation_listener_wakes_on_intent_and_finishes_before_later_snapshots() {
        use super::*;
        let (sender, receiver) = watch::channel(AgentStatus {
            state: AgentState::Online,
            cancellations: Arc::new(HashSet::from([1])),
        });
        let listener = tokio::spawn(wait_for_cancellation(receiver, 77));
        tokio::task::yield_now().await;
        assert!(!listener.is_finished());
        sender
            .send(AgentStatus {
                state: AgentState::Online,
                cancellations: Arc::new(HashSet::from([77])),
            })
            .unwrap();
        timeout(Duration::from_millis(1), listener)
            .await
            .unwrap()
            .unwrap();
        sender.send_replace(AgentStatus {
            state: AgentState::Online,
            cancellations: Arc::new(HashSet::new()),
        });
        // The completed listener is never polled again by monitor_job, so an
        // empty later snapshot cannot revoke already-observed cancel intent.
    }

    pub(super) fn proto_value(value: serde_json::Value) -> prost_types::Value {
        use prost_types::value::Kind;
        let kind = match value {
            serde_json::Value::Object(fields) => Kind::StructValue(prost_types::Struct {
                fields: fields
                    .into_iter()
                    .map(|(key, value)| (key, proto_value(value)))
                    .collect(),
            }),
            serde_json::Value::Number(value) => Kind::NumberValue(value.as_f64().unwrap()),
            serde_json::Value::String(value) => Kind::StringValue(value),
            serde_json::Value::Null => Kind::NullValue(0),
            serde_json::Value::Bool(value) => Kind::BoolValue(value),
            serde_json::Value::Array(values) => Kind::ListValue(prost_types::ListValue {
                values: values.into_iter().map(proto_value).collect(),
            }),
        };
        prost_types::Value { kind: Some(kind) }
    }

    mod preparation_regressions {
        use super::super::*;
        use super::{proto_value, temporary_workspace};
        use serde_json::json;
        use tokio::{net::TcpListener, sync::oneshot};

        fn config(work_root: PathBuf, url: reqwest::Url, executor: Executor) -> Config {
            Config {
                session_id: Uuid::new_v4().to_string(),
                partition: "test".to_owned(),
                executor,
                docker_socket: PathBuf::from("/var/run/docker.sock"),
                grpc_endpoint: "http://127.0.0.1:50051".to_owned(),
                grpc_server_name: None,
                insecure_grpc_dev: true,
                grpc_ca_file: None,
                token: "test".to_owned(),
                control_plane_http_url: url,
                work_root,
                idle_poll: Duration::from_secs(1),
            }
        }

        async fn http_peer(
            response: Vec<u8>,
        ) -> (reqwest::Url, oneshot::Receiver<()>, JoinHandle<()>) {
            let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
            let url =
                reqwest::Url::parse(&format!("http://{}", listener.local_addr().unwrap())).unwrap();
            let (ready, received) = oneshot::channel();
            let server = tokio::spawn(async move {
                let (mut stream, _) = listener.accept().await.unwrap();
                let mut request = Vec::new();
                let mut bytes = [0; 4096];
                while !request.windows(4).any(|bytes| bytes == b"\r\n\r\n") {
                    let count = stream.read(&mut bytes).await.unwrap();
                    assert_ne!(count, 0);
                    request.extend_from_slice(&bytes[..count]);
                }
                stream.write_all(&response).await.unwrap();
                ready.send(()).unwrap();
                std::future::pending::<()>().await;
                drop(stream);
            });
            (url, received, server)
        }

        async fn wait_for_bytes(path: &Path) {
            timeout(Duration::from_secs(5), async {
                loop {
                    if std::fs::metadata(path).is_ok_and(|metadata| metadata.len() > 0) {
                        break;
                    }
                    tokio::task::yield_now().await;
                }
            })
            .await
            .expect("transfer or extraction never started");
        }

        #[test]
        fn result_message_may_be_absent_but_must_not_be_null_or_another_type() {
            let result = json!({"schemaVersion":1,"jobId":77,"verdict":"accepted","score":"1"});
            assert!(validate_result(&serde_json::to_vec(&result).unwrap(), 77)
                .unwrap()
                .message
                .is_none());
            let mut present = result.clone();
            present["message"] = json!("accepted");
            assert_eq!(
                validate_result(&serde_json::to_vec(&present).unwrap(), 77)
                    .unwrap()
                    .message
                    .as_deref(),
                Some("accepted")
            );
            for message in [json!(null), json!(0), json!(false), json!([]), json!({})] {
                let mut invalid = result.clone();
                invalid["message"] = message;
                assert!(validate_result(&serde_json::to_vec(&invalid).unwrap(), 77).is_err());
            }
        }

        #[test]
        fn active_workspace_scans_allow_file_and_queued_directory_churn() {
            let work = temporary_workspace("live-workspace-churn");
            for index in 0..32 {
                let directory = work.join(index.to_string());
                std::fs::create_dir(&directory).unwrap();
                for child in 0..32 {
                    std::fs::write(directory.join(child.to_string()), []).unwrap();
                }
            }
            let stop = Arc::new(AtomicBool::new(false));
            let writer = std::thread::spawn({
                let work = work.clone();
                let stop = Arc::clone(&stop);
                move || {
                    while !stop.load(Ordering::Acquire) {
                        for index in 0..32 {
                            let directory = work.join(index.to_string());
                            std::fs::remove_dir_all(&directory).unwrap();
                            std::fs::create_dir(&directory).unwrap();
                            for child in 0..32 {
                                std::fs::write(directory.join(child.to_string()), []).unwrap();
                            }
                        }
                    }
                }
            });
            let mut live_error = None;
            let mut strict_saw_disappearance = false;
            for _ in 0..1000 {
                if let Err(error) = workspace_within_limit(&work, WorkspaceScan::Active) {
                    live_error = Some(error);
                    break;
                }
                if let Err(error) = workspace_within_limit(&work, WorkspaceScan::Final) {
                    assert_eq!(
                        error.downcast_ref::<std::io::Error>().unwrap().kind(),
                        std::io::ErrorKind::NotFound
                    );
                    strict_saw_disappearance = true;
                }
            }
            stop.store(true, Ordering::Release);
            writer.join().unwrap();
            let final_result = workspace_within_limit(&work, WorkspaceScan::Final);
            std::fs::remove_dir_all(work).unwrap();
            assert!(
                strict_saw_disappearance,
                "churn did not exercise a disappearance"
            );
            assert!(
                live_error.is_none(),
                "live scan rejected normal churn: {live_error:?}"
            );
            final_result.unwrap();
        }

        #[cfg(unix)]
        #[test]
        fn active_workspace_scans_still_reject_special_files_and_missing_roots() {
            let work = temporary_workspace("live-workspace-errors");
            let socket = std::os::unix::net::UnixListener::bind(work.join("socket")).unwrap();
            for scan in [WorkspaceScan::Active, WorkspaceScan::Final] {
                assert!(workspace_within_limit(&work, scan)
                    .unwrap_err()
                    .to_string()
                    .contains("special file"));
            }
            drop(socket);
            std::fs::remove_dir_all(&work).unwrap();
            for scan in [WorkspaceScan::Active, WorkspaceScan::Final] {
                assert!(workspace_within_limit(&work, scan).is_err());
            }
        }

        #[tokio::test]
        async fn stalled_download_headers_and_body_have_finite_waits() {
            for response in [
                b"".as_slice(),
                b"HTTP/1.1 200 OK\r\nContent-Length: 6\r\n\r\nabc".as_slice(),
            ] {
                let work = temporary_workspace("download-stall");
                let destination = work.join("artifact");
                let (url, received, server) = http_peer(response.to_vec()).await;
                let config = config(work.clone(), url, Executor::Slurm);
                let transfer = tokio::spawn({
                    let destination = destination.clone();
                    async move {
                        download_artifact(
                            &config,
                            77,
                            1,
                            &format!("{:x}", Sha256::digest(b"abcdef")),
                            6,
                            &destination,
                            std::future::pending(),
                        )
                        .await
                    }
                });
                timeout(Duration::from_secs(5), received)
                    .await
                    .unwrap()
                    .unwrap();
                if !response.is_empty() {
                    wait_for_bytes(&destination).await;
                }
                tokio::time::pause();
                tokio::time::advance(ARTIFACT_READ_TIMEOUT + Duration::from_secs(1)).await;
                tokio::time::resume();
                let error = timeout(Duration::from_secs(5), transfer)
                    .await
                    .expect("stalled download did not time out")
                    .unwrap()
                    .unwrap_err();
                assert!(error.downcast_ref::<reqwest::Error>().unwrap().is_timeout());
                assert!(!destination.exists(), "partial download survived failure");
                server.abort();
                let _ = server.await;
                std::fs::remove_dir_all(work).unwrap();
            }
        }

        #[tokio::test]
        async fn continuously_dribbling_upload_response_still_has_a_total_deadline() {
            let work = temporary_workspace("upload-deadline");
            let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
            let url =
                reqwest::Url::parse(&format!("http://{}", listener.local_addr().unwrap())).unwrap();
            let config = config(work.clone(), url, Executor::Slurm);
            let (ready, received) = oneshot::channel();
            let (progress, mut chunks) = tokio::sync::mpsc::unbounded_channel();
            let server = tokio::spawn(async move {
                let (mut stream, _) = listener.accept().await.unwrap();
                let mut request = [0; 4096];
                assert_ne!(stream.read(&mut request).await.unwrap(), 0);
                stream
                    .write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 4096\r\n\r\n{")
                    .await
                    .unwrap();
                ready.send(()).unwrap();
                loop {
                    tokio::time::sleep(ARTIFACT_READ_TIMEOUT / 2).await;
                    if stream.write_all(b" ").await.is_err() {
                        break;
                    }
                    let _ = progress.send(());
                }
            });
            let transfer =
                tokio::spawn(
                    async move { upload_artifact(&config, 77, "judge_log", vec![b'x']).await },
                );
            timeout(Duration::from_secs(5), received)
                .await
                .unwrap()
                .unwrap();
            tokio::task::yield_now().await;
            tokio::time::pause();
            let step = ARTIFACT_READ_TIMEOUT / 2;
            for _ in 1..ARTIFACT_UPLOAD_TIMEOUT.as_secs() / step.as_secs() {
                tokio::time::advance(step).await;
                timeout(Duration::from_secs(1), chunks.recv())
                    .await
                    .unwrap()
                    .unwrap();
                tokio::task::yield_now().await;
                assert!(
                    !transfer.is_finished(),
                    "upload stopped despite ongoing progress"
                );
            }
            tokio::time::advance(step + Duration::from_secs(1)).await;
            tokio::time::resume();
            let error = timeout(Duration::from_secs(5), transfer)
                .await
                .expect("upload ignored its total deadline")
                .unwrap()
                .unwrap_err();
            assert!(error.downcast_ref::<reqwest::Error>().unwrap().is_timeout());
            server.abort();
            let _ = server.await;
            std::fs::remove_dir_all(work).unwrap();
        }

        #[tokio::test]
        async fn stalled_optional_log_upload_does_not_block_terminal_completion() {
            let work = temporary_workspace("log-upload-stall");
            std::fs::create_dir(work.join("output")).unwrap();
            std::fs::write(work.join("output/judge.log"), b"judge diagnostics").unwrap();
            let (url, received, server) = http_peer(Vec::new()).await;
            let config = config(work.clone(), url, Executor::Slurm);
            let (mut control, mut calls, grpc) = recording_control().await;
            let completion = tokio::spawn({
                let work = work.clone();
                async move {
                    terminal_complete(
                        &config,
                        &mut control,
                        77,
                        &work,
                        ExecutionTerminalState::OutOfMemory,
                        StructuredResult {
                            verdict: Verdict::MemoryLimit as i32,
                            score: "0".to_owned(),
                            message: None,
                        },
                        None,
                    )
                    .await
                }
            });
            timeout(Duration::from_secs(5), received)
                .await
                .unwrap()
                .unwrap();
            tokio::time::pause();
            tokio::time::advance(ARTIFACT_READ_TIMEOUT + Duration::from_secs(1)).await;
            tokio::time::resume();
            timeout(Duration::from_secs(5), completion)
                .await
                .expect("optional log upload blocked completion indefinitely")
                .unwrap()
                .unwrap();
            assert_eq!(
                calls.try_recv().unwrap(),
                "/soj.judge.v1.JudgeControlService/CompleteJob"
            );
            assert!(calls.try_recv().is_err());
            server.abort();
            grpc.abort();
            let _ = server.await;
            let _ = grpc.await;
            std::fs::remove_dir_all(work).unwrap();
        }

        #[derive(Clone)]
        struct RecordingControl {
            calls: tokio::sync::mpsc::UnboundedSender<String>,
        }

        impl tonic::server::NamedService for RecordingControl {
            const NAME: &'static str = "soj.judge.v1.JudgeControlService";
        }

        struct CancelRpc;

        impl tonic::server::UnaryService<CancelJobRequest> for CancelRpc {
            type Response = soj::judge::v1::CancelJobResponse;
            type Future = tonic::codegen::BoxFuture<tonic::Response<Self::Response>, tonic::Status>;

            fn call(&mut self, request: Request<CancelJobRequest>) -> Self::Future {
                assert_eq!(request.get_ref().job_id, 77);
                Box::pin(async {
                    Ok(tonic::Response::new(soj::judge::v1::CancelJobResponse {
                        state: JobState::Cancelled as i32,
                    }))
                })
            }
        }

        struct CompleteRpc;

        impl tonic::server::UnaryService<CompleteJobRequest> for CompleteRpc {
            type Response = soj::judge::v1::CompleteJobResponse;
            type Future = tonic::codegen::BoxFuture<tonic::Response<Self::Response>, tonic::Status>;

            fn call(&mut self, request: Request<CompleteJobRequest>) -> Self::Future {
                let request = request.into_inner();
                assert_eq!(request.job_id, 77);
                assert_eq!(
                    request.execution_terminal_state,
                    ExecutionTerminalState::OutOfMemory as i32
                );
                assert_eq!(request.result.unwrap().verdict, Verdict::MemoryLimit as i32);
                assert!(request.result_artifact_id.is_none());
                assert!(request.log_artifact_id.is_none());
                Box::pin(async {
                    Ok(tonic::Response::new(soj::judge::v1::CompleteJobResponse {
                        state: JobState::Completed as i32,
                    }))
                })
            }
        }

        impl tonic::codegen::Service<tonic::codegen::http::Request<tonic::body::BoxBody>>
            for RecordingControl
        {
            type Response = tonic::codegen::http::Response<tonic::body::BoxBody>;
            type Error = std::convert::Infallible;
            type Future = tonic::codegen::BoxFuture<Self::Response, Self::Error>;

            fn poll_ready(
                &mut self,
                _: &mut std::task::Context<'_>,
            ) -> std::task::Poll<std::result::Result<(), Self::Error>> {
                std::task::Poll::Ready(Ok(()))
            }

            fn call(
                &mut self,
                request: tonic::codegen::http::Request<tonic::body::BoxBody>,
            ) -> Self::Future {
                let path = request.uri().path().to_owned();
                self.calls.send(path.clone()).unwrap();
                Box::pin(async move {
                    if path == "/soj.judge.v1.JudgeControlService/CancelJob" {
                        let mut grpc =
                            tonic::server::Grpc::new(tonic::codec::ProstCodec::default());
                        Ok(grpc.unary(CancelRpc, request).await)
                    } else if path == "/soj.judge.v1.JudgeControlService/CompleteJob" {
                        let mut grpc =
                            tonic::server::Grpc::new(tonic::codec::ProstCodec::default());
                        Ok(grpc.unary(CompleteRpc, request).await)
                    } else {
                        Ok(tonic::codegen::http::Response::builder()
                            .status(200)
                            .header("content-type", "application/grpc")
                            .header("grpc-status", "12")
                            .body(tonic::body::empty_body())
                            .unwrap())
                    }
                })
            }
        }

        async fn recording_control() -> (
            Control,
            tokio::sync::mpsc::UnboundedReceiver<String>,
            JoinHandle<()>,
        ) {
            let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
            let endpoint = format!("http://{}", listener.local_addr().unwrap());
            let (calls, received_calls) = tokio::sync::mpsc::unbounded_channel();
            let service = RecordingControl { calls };
            let incoming = futures_util::stream::unfold(listener, |listener| async move {
                Some((listener.accept().await.map(|(stream, _)| stream), listener))
            });
            let server = tokio::spawn(async move {
                tonic::transport::Server::builder()
                    .add_service(service)
                    .serve_with_incoming(incoming)
                    .await
                    .unwrap();
            });
            let control = Control {
                client: JudgeControlServiceClient::new(
                    Endpoint::from_shared(endpoint)
                        .unwrap()
                        .connect()
                        .await
                        .unwrap(),
                ),
                bearer: "Bearer test".parse().unwrap(),
                mutations: Arc::new(Mutex::new(())),
            };
            (control, received_calls, server)
        }

        fn package_fixture(executor: Executor) -> (Vec<u8>, prost_types::Struct) {
            let mut manifest = json!({"schemaVersion":1,"problemId":1});
            if executor == Executor::Docker {
                manifest["docker"] = json!({
                    "image":format!("sha256:{}", "a".repeat(64)),
                    "cpus":1,"memoryMb":128,"timeLimitSeconds":10
                });
            }
            let encoder = zstd::stream::write::Encoder::new(Vec::new(), 1).unwrap();
            let mut archive = tar::Builder::new(encoder);
            for (path, bytes) in [
                ("manifest.json", serde_json::to_vec(&manifest).unwrap()),
                (
                    if executor == Executor::Docker {
                        "judge.sh"
                    } else {
                        "judge.sbatch"
                    },
                    b"#!/bin/sh\n".to_vec(),
                ),
            ] {
                let mut header = tar::Header::new_gnu();
                header.set_size(bytes.len() as u64);
                header.set_mode(0o600);
                archive
                    .append_data(&mut header, path, bytes.as_slice())
                    .unwrap();
            }
            let mut header = tar::Header::new_gnu();
            header.set_size(256 << 20);
            header.set_mode(0o600);
            archive
                .append_data(&mut header, "payload", std::io::repeat(0).take(256 << 20))
                .unwrap();
            let bytes = archive.into_inner().unwrap().finish().unwrap();
            let prost_types::value::Kind::StructValue(snapshot) =
                proto_value(manifest).kind.unwrap()
            else {
                unreachable!()
            };
            (bytes, snapshot)
        }

        #[tokio::test]
        async fn unpack_cancellation_interrupts_a_large_entry_and_joins_its_writer() {
            let work = temporary_workspace("unpack-cancellation");
            let package = work.join("package");
            std::fs::create_dir(&package).unwrap();
            let (bytes, _) = package_fixture(Executor::Slurm);
            let archive = work.join("package.tar.zst");
            std::fs::write(&archive, bytes).unwrap();
            let cancelled = Arc::new(AtomicBool::new(false));
            let extraction = tokio::spawn(unpack_package(
                archive,
                package.clone(),
                Arc::clone(&cancelled),
            ));
            let payload = package.join("payload");
            wait_for_bytes(&payload).await;
            cancelled.store(true, Ordering::Release);
            assert!(timeout(Duration::from_secs(5), extraction)
                .await
                .expect("blocking extraction did not stop")
                .unwrap()
                .is_err());
            assert!(std::fs::metadata(payload).unwrap().len() < (256 << 20));
            cleanup_workspace(&work).await;
            assert!(!work.exists());
        }

        #[tokio::test]
        async fn preparing_jobs_cancel_downloads_and_join_extraction_on_both_backends() {
            for executor in [Executor::Slurm, Executor::Docker] {
                let (package, manifest) = package_fixture(executor);
                for extracting in [false, true] {
                    let root = temporary_workspace("cancel-preparation");
                    let work = root.join("77");
                    let response = if extracting {
                        let mut response = format!(
                            "HTTP/1.1 200 OK\r\nContent-Length: {}\r\n\r\n",
                            package.len()
                        )
                        .into_bytes();
                        response.extend_from_slice(&package);
                        response
                    } else {
                        Vec::new()
                    };
                    let (url, received, http) = http_peer(response).await;
                    let config = config(root.clone(), url, executor);
                    let assignment = JobAssignment {
                        job_id: 77,
                        submission_id: 1,
                        agent_session_id: config.session_id.clone(),
                        assigned_partition: config.partition.clone(),
                        executor: executor.protocol() as i32,
                        package: Some(soj::judge::v1::ArtifactReference {
                            artifact_id: 1,
                            sha256: format!("{:x}", Sha256::digest(&package)),
                            size_bytes: package.len() as u64,
                        }),
                        submission: Some(soj::judge::v1::ArtifactReference {
                            artifact_id: 2,
                            sha256: format!("{:x}", Sha256::digest(b"source")),
                            size_bytes: 6,
                        }),
                        manifest: Some(manifest.clone()),
                        submission_filename: "source.txt".to_owned(),
                        submission_content_type: "text/plain".to_owned(),
                        ..JobAssignment::default()
                    };
                    let (mut control, mut calls, grpc) = recording_control().await;
                    let (status, receiver) = watch::channel(AgentStatus {
                        state: AgentState::Online,
                        cancellations: Arc::new(HashSet::new()),
                    });
                    let waiting = Arc::new(AtomicBool::new(true));
                    let mut job = tokio::spawn({
                        let waiting = Arc::clone(&waiting);
                        async move {
                            process_assignment(&config, &mut control, assignment, receiver, waiting)
                                .await
                        }
                    });
                    timeout(Duration::from_secs(5), received)
                        .await
                        .unwrap()
                        .unwrap();
                    if extracting {
                        wait_for_bytes(&work.join("package/payload")).await;
                    } else {
                        status.send_replace(AgentStatus {
                            state: AgentState::Draining,
                            cancellations: Arc::new(HashSet::new()),
                        });
                        assert!(timeout(Duration::from_millis(25), &mut job).await.is_err());
                    }
                    status.send_replace(AgentStatus {
                        state: AgentState::Draining,
                        cancellations: Arc::new(HashSet::from([77])),
                    });
                    timeout(Duration::from_secs(5), job)
                        .await
                        .expect("preparation did not converge after cancellation")
                        .unwrap()
                        .unwrap();
                    assert_eq!(
                        calls.try_recv().unwrap(),
                        "/soj.judge.v1.JudgeControlService/CancelJob"
                    );
                    assert!(
                        calls.try_recv().is_err(),
                        "unexpected additional terminal RPC"
                    );
                    assert!(!waiting.load(Ordering::Acquire));
                    assert!(
                        !work.exists(),
                        "cancelled preparation retained its workspace"
                    );
                    http.abort();
                    grpc.abort();
                    let _ = http.await;
                    let _ = grpc.await;
                    std::fs::remove_dir_all(root).unwrap();
                }
            }
        }
    }
}
