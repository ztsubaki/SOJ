use anyhow::{bail, Context, Result};
use futures_util::StreamExt;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
#[cfg(unix)]
use std::os::unix::fs::OpenOptionsExt;
use std::{
    collections::HashMap,
    io::Read,
    net::IpAddr,
    path::Component,
    path::{Path, PathBuf},
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};
use tokio::{
    fs::File,
    io::AsyncWriteExt,
    process::Command,
    sync::{watch, Mutex},
    task::JoinSet,
    time::{interval, MissedTickBehavior},
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
    v1::{AgentState, JobState, SlurmTerminalState, Verdict},
};

const RPC_TIMEOUT: Duration = Duration::from_secs(8);
const MAX_UNPACKED_PACKAGE_BYTES: u64 = 10 << 30;
const MAX_GRPC_MESSAGE_BYTES: usize = 64 << 20;
const MAX_PACKAGE_FILES: u64 = 10_000;
const MAX_RESULT_BYTES: u64 = 1 << 20;
const MAX_LOG_BYTES: u64 = 64 << 20;
const MAX_LOG_COMPONENT_BYTES: u64 = 8 << 20;
// Leave room for the unpacked package plus source and execution outputs.
const MAX_WORKSPACE_BYTES: u64 = 20 << 30;
const MAX_WORKSPACE_FILES: u64 = 20_000;
const SCHEDULER_POLL: Duration = Duration::from_secs(1);
const IDLE_DIAGNOSTIC_INTERVAL: Duration = Duration::from_secs(30);

struct Config {
    session_id: String,
    partition: String,
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
        Ok(Self {
            session_id,
            partition,
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
            }))
            .await?
            .into_inner();
        let session = response
            .session
            .context("RegisterAgent returned no session")?;
        if session.id != config.session_id || session.partition != config.partition {
            bail!("RegisterAgent returned a different session identity");
        }
        live(AgentState::try_from(session.state).unwrap_or(AgentState::Unspecified))?;
        let duration = Duration::from_millis(response.heartbeat_interval_ms);
        if duration.is_zero() {
            bail!("RegisterAgent returned a zero heartbeat interval")
        }
        Ok(duration)
    }
    async fn heartbeat(&mut self, id: &str, partition: &str) -> Result<AgentStatus> {
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
                    }))
                    .await?
                    .into_inner()
                    .session
                    .context("missing Agent session")?;
                if session.state == AgentState::Ended as i32 {
                    return Ok(AgentStatus {
                        state: AgentState::Ended,
                        cancellations: vec![],
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
            cancellations: response.cancel_job_ids,
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
        slurm_id: Option<&str>,
    ) -> Result<JobState> {
        let _mutation = self.mutations.lock().await;
        let response = self
            .client
            .report_job(self.request(ReportJobRequest {
                agent_session_id: agent_id.to_owned(),
                job_id,
                state: state as i32,
                slurm_job_id: slurm_id.map(ToOwned::to_owned),
                observed_at: Some(now_timestamp()?),
            }))
            .await?;
        Ok(JobState::try_from(response.into_inner().state).unwrap_or(JobState::Unspecified))
    }
    async fn complete(
        &mut self,
        agent_id: &str,
        job_id: i64,
        terminal: SlurmTerminalState,
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
                slurm_terminal_state: terminal as i32,
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
    cancellations: Vec<i64>,
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
        f.write_str("sbatch succeeded but returned an invalid job ID; retaining workspace")
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

impl SlurmCli {
    fn command(binary: &str) -> Command {
        let mut command = Command::new(binary);
        command.env_clear().env("PATH", "/usr/bin:/bin");
        command
    }
    async fn submit(partition: &str, work: &Path) -> Result<String> {
        let script = work.join("package/judge.sbatch");
        let output = Self::command("sbatch")
            .args([
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
            ])
            .output()
            .await
            .context("run sbatch")?;
        if !output.status.success() {
            bail!(
                "sbatch failed: {}",
                String::from_utf8_lossy(&output.stderr).trim()
            )
        }
        submitted_job_id(&output.stdout)
    }
    async fn cancel(job_id: &str) -> Result<()> {
        let output = Self::command("scancel")
            .arg(job_id)
            .output()
            .await
            .context("run scancel")?;
        if !output.status.success() {
            bail!(
                "scancel failed: {}",
                String::from_utf8_lossy(&output.stderr).trim()
            )
        }
        Ok(())
    }
    async fn status(job_id: &str) -> Result<Option<SchedulerState>> {
        let queue = Self::command("squeue")
            .args(["--json", "--jobs", job_id])
            .output()
            .await
            .context("run squeue")?;
        if !queue.status.success() {
            bail!(
                "squeue failed: {}",
                String::from_utf8_lossy(&queue.stderr).trim()
            );
        }
        if let Some(state) = parse_scheduler_json(&queue.stdout)? {
            return Ok(Some(state));
        }
        let accounting = Self::command("sacct")
            .args(["--json", "--jobs", job_id])
            .output()
            .await
            .context("run sacct")?;
        if !accounting.status.success() {
            bail!(
                "sacct failed: {}",
                String::from_utf8_lossy(&accounting.stderr).trim()
            );
        }
        parse_scheduler_json(&accounting.stdout)
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
    let raw = job
        .get("job_state")
        .and_then(json_state_string)
        .or_else(|| {
            job.get("state")
                .and_then(|v| v.get("current"))
                .and_then(json_state_string)
        })
        .context("Slurm job has no state")?;
    let state = raw
        .split(|c: char| c.is_ascii_whitespace() || c == '+')
        .next()
        .unwrap_or_default();
    let parsed = match state {
        "PENDING" | "CONFIGURING" => SchedulerState::Submitted,
        "RUNNING" => SchedulerState::Running,
        "COMPLETING" => SchedulerState::Finishing,
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

fn json_state_string(value: &serde_json::Value) -> Option<&str> {
    value
        .as_str()
        .or_else(|| value.as_array()?.first()?.as_str())
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Manifest {
    schema_version: u32,
    problem_id: i64,
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

async fn download_artifact(
    config: &Config,
    job_id: i64,
    artifact_id: i64,
    expected_sha256: &str,
    expected_size: u64,
    destination: &Path,
) -> Result<()> {
    if job_id <= 0 || artifact_id <= 0 {
        bail!("invalid Artifact download identifier");
    }
    let url = config.control_plane_http_url.join(&format!(
        "/internal/v1/judge/jobs/{job_id}/artifacts/{artifact_id}"
    ))?;
    let response = reqwest::Client::new()
        .get(url)
        .bearer_auth(&config.token)
        .header("X-SOJ-Agent-Session", &config.session_id)
        .send()
        .await?
        .error_for_status()?;
    let outcome = async {
        let mut file = File::create(destination).await?;
        let mut hash = Sha256::new();
        let mut size = 0_u64;
        let mut stream = response.bytes_stream();
        while let Some(chunk) = stream.next().await {
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

async fn unpack_package(archive: PathBuf, package: PathBuf) -> Result<()> {
    tokio::task::spawn_blocking(move || -> Result<()> {
        let file = std::fs::File::open(archive)?;
        let decoder = zstd::stream::read::Decoder::new(file)?;
        let mut tar = tar::Archive::new(decoder);
        let mut entries = tar.entries()?;
        let mut files = 0_u64;
        let mut unpacked = 0_u64;
        while let Some(entry) = entries.next() {
            let mut entry = entry?;
            let path = entry.path()?;
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
            files += u64::from(kind.is_file());
            if files > MAX_PACKAGE_FILES {
                bail!("package archive has too many files");
            }
            unpacked = unpacked
                .checked_add(entry.size())
                .context("package size overflow")?;
            if unpacked > MAX_UNPACKED_PACKAGE_BYTES {
                bail!("package archive expands beyond the configured limit");
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

fn assignment_manifest(assignment: &JobAssignment) -> Result<i64> {
    let manifest = assignment
        .manifest
        .as_ref()
        .context("assignment has no manifest")?;
    if manifest.fields.len() != 2 {
        bail!("assignment manifest has unexpected fields");
    }
    match manifest
        .fields
        .get("schemaVersion")
        .and_then(|value| value.kind.as_ref())
    {
        Some(prost_types::value::Kind::NumberValue(value)) if *value == 1.0 => {}
        _ => bail!("assignment manifest has invalid schemaVersion"),
    }
    let problem = match manifest
        .fields
        .get("problemId")
        .and_then(|value| value.kind.as_ref())
    {
        Some(prost_types::value::Kind::NumberValue(value))
            if value.is_finite()
                && *value > 0.0
                && *value <= 9_007_199_254_740_991.0
                && value.fract() == 0.0 =>
        {
            *value as i64
        }
        _ => bail!("assignment manifest has invalid problemId"),
    };
    Ok(problem)
}

async fn validate_package(package: &Path, expected_problem: &i64) -> Result<()> {
    let manifest_path = package.join("manifest.json");
    let script_path = package.join("judge.sbatch");
    for path in [&manifest_path, &script_path] {
        let metadata = tokio::fs::symlink_metadata(path).await?;
        if metadata.file_type().is_symlink() || !metadata.is_file() {
            bail!("package required entry is not a regular file");
        }
    }
    let bytes = tokio::fs::read(&manifest_path).await?;
    if bytes.len() > 64 << 10 {
        bail!("package manifest is too large");
    }
    let manifest: Manifest = serde_json::from_slice(&bytes).context("invalid package manifest")?;
    if manifest.schema_version != 1
        || manifest.problem_id <= 0
        || manifest.problem_id != *expected_problem
    {
        bail!("package manifest does not match assignment");
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
    let response = reqwest::Client::new()
        .post(url)
        .bearer_auth(&config.token)
        .header("X-SOJ-Agent-Session", &config.session_id)
        .header(reqwest::header::CONTENT_TYPE, "application/octet-stream")
        .body(bytes)
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
    message: Option<String>,
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

fn workspace_within_limit(work: &Path) -> Result<()> {
    fn visit(path: &Path, bytes: &mut u64, files: &mut u64) -> Result<()> {
        let metadata = std::fs::symlink_metadata(path)?;
        if metadata.file_type().is_symlink() {
            return Ok(());
        }
        if metadata.is_file() {
            *files = files
                .checked_add(1)
                .context("workspace file count overflow")?;
            *bytes = bytes
                .checked_add(metadata.len())
                .context("workspace size overflow")?;
            if *files > MAX_WORKSPACE_FILES || *bytes > MAX_WORKSPACE_BYTES {
                bail!("workspace exceeds configured limit");
            }
            return Ok(());
        }
        if !metadata.is_dir() {
            bail!("workspace contains a special file");
        }
        for entry in std::fs::read_dir(path)? {
            visit(&entry?.path(), bytes, files)?;
        }
        Ok(())
    }
    let mut bytes = 0;
    let mut files = 0;
    visit(work, &mut bytes, &mut files)
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

async fn stage_assignment(config: &Config, assignment: &JobAssignment) -> Result<PathBuf> {
    if assignment.job_id <= 0
        || assignment.submission_id <= 0
        || assignment.agent_session_id != config.session_id
        || assignment.assigned_partition != config.partition
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
    let expected_problem = assignment_manifest(assignment)?;
    let (package_id, package_sha, package_size) =
        artifact_reference(assignment.package.as_ref(), "package")?;
    let (submission_id, submission_sha, submission_size) =
        artifact_reference(assignment.submission.as_ref(), "submission")?;
    let work = create_job_dir(config, assignment.job_id).await?;
    let package_archive = work.join("package.tar.zst");
    let staged = async {
        download_artifact(
            config,
            assignment.job_id,
            package_id,
            package_sha,
            package_size,
            &package_archive,
        )
        .await?;
        unpack_package(package_archive.clone(), work.join("package")).await?;
        validate_package(&work.join("package"), &expected_problem).await?;
        download_artifact(
            config,
            assignment.job_id,
            submission_id,
            submission_sha,
            submission_size,
            &work.join("submission/source"),
        )
        .await?;
        write_job_context(
            &work,
            assignment.job_id,
            &assignment.submission_filename,
            &assignment.submission_content_type,
        )
        .await
    }
    .await;
    let _ = tokio::fs::remove_file(package_archive).await;
    if let Err(error) = staged {
        cleanup_workspace(&work).await;
        return Err(error);
    }
    Ok(work)
}

async fn terminal_complete(
    config: &Config,
    control: &mut Control,
    job_id: i64,
    work: &Path,
    terminal: SlurmTerminalState,
    result: StructuredResult,
    result_bytes: Option<Vec<u8>>,
) -> Result<()> {
    control
        .report(&config.session_id, job_id, JobState::Finishing, None)
        .await?;
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

async fn monitor_job(
    config: &Config,
    control: &mut Control,
    assignment: &JobAssignment,
    work: &Path,
    slurm_id: &str,
    status: watch::Receiver<AgentStatus>,
    waiting: &AtomicBool,
) -> Result<()> {
    let mut cancellation_requested = false;
    let mut workspace_error = None;
    let mut poll = interval(SCHEDULER_POLL);
    poll.set_missed_tick_behavior(MissedTickBehavior::Delay);
    loop {
        poll.tick().await;
        cancellation_requested |= status.borrow().cancellations.contains(&assignment.job_id);
        if workspace_error.is_none() {
            let path = work.to_owned();
            workspace_error = tokio::task::spawn_blocking(move || workspace_within_limit(&path))
                .await?
                .err()
                .map(|error| format!("workspace limit violation: {error:#}"));
        }
        if cancellation_requested || workspace_error.is_some() {
            if let Err(error) = SlurmCli::cancel(slurm_id).await {
                eprintln!("warning: cannot cancel Slurm job {slurm_id}: {error:#}");
            }
        }
        let Some(state) = SlurmCli::status(slurm_id).await? else {
            continue;
        };
        // Never clean a workspace until Slurm confirms that its writers stopped.
        if !matches!(
            state,
            SchedulerState::Submitted | SchedulerState::Running | SchedulerState::Finishing
        ) {
            if let Some(message) = &workspace_error {
                control
                    .fail(&config.session_id, assignment.job_id, message)
                    .await?;
                waiting.store(false, Ordering::Release);
                return Ok(());
            }
        }
        match state {
            SchedulerState::Submitted => {
                waiting.store(true, Ordering::Release);
                control
                    .report(
                        &config.session_id,
                        assignment.job_id,
                        JobState::Submitted,
                        Some(slurm_id),
                    )
                    .await?;
            }
            SchedulerState::Running => {
                control
                    .report(
                        &config.session_id,
                        assignment.job_id,
                        JobState::Running,
                        Some(slurm_id),
                    )
                    .await?;
                waiting.store(false, Ordering::Release);
            }
            SchedulerState::Finishing => {
                control
                    .report(
                        &config.session_id,
                        assignment.job_id,
                        JobState::Finishing,
                        Some(slurm_id),
                    )
                    .await?;
                waiting.store(false, Ordering::Release);
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
            SchedulerState::Timeout => {
                control
                    .report(
                        &config.session_id,
                        assignment.job_id,
                        JobState::Finishing,
                        Some(slurm_id),
                    )
                    .await?;
                waiting.store(false, Ordering::Release);
                terminal_complete(
                    config,
                    control,
                    assignment.job_id,
                    work,
                    SlurmTerminalState::Timeout,
                    StructuredResult {
                        verdict: Verdict::TimeLimit as i32,
                        score: "0".to_owned(),
                        message: None,
                    },
                    None,
                )
                .await?;
                return Ok(());
            }
            SchedulerState::OutOfMemory => {
                control
                    .report(
                        &config.session_id,
                        assignment.job_id,
                        JobState::Finishing,
                        Some(slurm_id),
                    )
                    .await?;
                waiting.store(false, Ordering::Release);
                terminal_complete(
                    config,
                    control,
                    assignment.job_id,
                    work,
                    SlurmTerminalState::OutOfMemory,
                    StructuredResult {
                        verdict: Verdict::MemoryLimit as i32,
                        score: "0".to_owned(),
                        message: None,
                    },
                    None,
                )
                .await?;
                return Ok(());
            }
            SchedulerState::Completed | SchedulerState::Failed => {
                control
                    .report(
                        &config.session_id,
                        assignment.job_id,
                        JobState::Finishing,
                        Some(slurm_id),
                    )
                    .await?;
                waiting.store(false, Ordering::Release);
                let output_work = work.to_owned();
                let result_bytes = match tokio::task::spawn_blocking(move || {
                    read_regular_file(
                        &output_work,
                        Path::new("output/result.json"),
                        MAX_RESULT_BYTES,
                    )
                })
                .await?
                {
                    Ok(Some(bytes)) => bytes,
                    Ok(None) => {
                        control
                            .fail(
                                &config.session_id,
                                assignment.job_id,
                                "Slurm completed without output/result.json",
                            )
                            .await?;
                        return Ok(());
                    }
                    Err(error) => {
                        control
                            .fail(
                                &config.session_id,
                                assignment.job_id,
                                &format!("invalid output/result.json: {error:#}"),
                            )
                            .await?;
                        return Ok(());
                    }
                };
                let result = match validate_result(&result_bytes, assignment.job_id) {
                    Ok(result) => result,
                    Err(error) => {
                        control
                            .fail(
                                &config.session_id,
                                assignment.job_id,
                                &format!("invalid output/result.json: {error:#}"),
                            )
                            .await?;
                        return Ok(());
                    }
                };
                let terminal = if state == SchedulerState::Completed {
                    SlurmTerminalState::Completed
                } else {
                    SlurmTerminalState::Failed
                };
                terminal_complete(
                    config,
                    control,
                    assignment.job_id,
                    work,
                    terminal,
                    result,
                    Some(result_bytes),
                )
                .await?;
                return Ok(());
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
    let work = match stage_assignment(config, &assignment).await {
        Ok(work) => work,
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
    let slurm_id = match SlurmCli::submit(&assignment.assigned_partition, &work).await {
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
    control
        .report(
            &config.session_id,
            assignment.job_id,
            JobState::Submitted,
            Some(&slurm_id),
        )
        .await?;
    monitor_job(
        config,
        control,
        &assignment,
        &work,
        &slurm_id,
        status,
        &waiting,
    )
    .await?;
    waiting.store(false, Ordering::Release);
    cleanup_workspace(&work).await;
    Ok(())
}

async fn run(config: Config) -> Result<()> {
    if config.control_plane_http_url.scheme() == "http" {
        eprintln!("WARNING: Judge Agent is using unencrypted Artifact HTTP; use only on a trusted isolated network");
    }
    eprintln!(
        "Judge Agent Artifact endpoint: {}",
        config.control_plane_http_url
    );
    tokio::fs::create_dir_all(&config.work_root).await?;
    let mut control = Control::connect(&config).await?;
    let heartbeat_interval = control.register(&config).await?;
    eprintln!(
        "registered Judge Agent {} for partition {}",
        config.session_id, config.partition
    );
    let config = Arc::new(config);
    let (status_tx, status_rx) = watch::channel(AgentStatus {
        state: AgentState::Online,
        cancellations: Vec::new(),
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
                .heartbeat(&config.session_id, &config.partition)
                .await
        }
    }));
    let mut jobs = JobTasks::default();
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
                                "no pending Job eligible for partition {}; {} pending Job(s) use another partition",
                                config.partition, claim.pending_job_count
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
    Fut: std::future::Future<Output = Result<AgentStatus>>,
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
}

impl JobTasks {
    fn is_empty(&self) -> bool {
        self.tasks.is_empty()
    }

    fn can_claim(&self, stopping: bool, state: AgentState) -> bool {
        !stopping
            && state == AgentState::Online
            && !self
                .waiting
                .values()
                .any(|waiting| waiting.load(Ordering::Acquire))
    }

    fn spawn<F, Fut>(&mut self, id: i64, worker: F) -> Result<()>
    where
        F: FnOnce(Arc<AtomicBool>) -> Fut,
        Fut: std::future::Future<Output = Result<()>> + Send + 'static,
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
        MAX_WORKSPACE_BYTES,
    };
    use std::{
        fs,
        path::PathBuf,
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
            cancellations: vec![],
        });
        let mut heartbeats = JoinSet::new();
        heartbeats.spawn(heartbeat_loop(Duration::from_secs(5), tx, || async {
            Ok(AgentStatus {
                state: AgentState::Online,
                cancellations: vec![1],
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
            assert_eq!(rx.borrow_and_update().cancellations, vec![1]);
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
            cancellations: vec![],
        });
        let task = tokio::spawn(heartbeat_loop(Duration::from_millis(1), tx, {
            let mut states = [AgentState::Draining, AgentState::Ended].into_iter();
            move || {
                let state = states.next().unwrap();
                async move {
                    Ok(AgentStatus {
                        state,
                        cancellations: vec![5],
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
            cancellations: vec![],
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
        assert!(workspace_within_limit(&work).is_err());
        fs::remove_dir_all(&work).unwrap();
    }
}
