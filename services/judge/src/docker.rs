use super::*;
use serde::Deserializer;
use std::ffi::OsString;
use tokio::io::AsyncRead;

const CLI_TIMEOUT: Duration = Duration::from_secs(10);
const STOP_CONFIRM_TIMEOUT: Duration = Duration::from_secs(20);
const RESPONSE_LIMIT: usize = 1 << 20;
const SESSION_LABEL: &str = "soj.agent-session";
const JOB_LABEL: &str = "soj.job";

#[derive(Debug, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct DockerManifest {
    image: String,
    cpus: u32,
    memory_mb: u64,
    time_limit_seconds: u64,
}

pub(super) fn present_manifest<'de, D>(
    deserializer: D,
) -> std::result::Result<Option<DockerManifest>, D::Error>
where
    D: Deserializer<'de>,
{
    // Unlike Option's normal deserializer, an explicitly present null is invalid.
    DockerManifest::deserialize(deserializer).map(Some)
}

impl DockerManifest {
    pub(super) fn validate(&self) -> Result<()> {
        let immutable = if let Some(digest) = self.image.strip_prefix("sha256:") {
            digest_hex(digest)
        } else if let Some((repository, digest)) = self.image.rsplit_once("@sha256:") {
            !repository.is_empty() && !repository.contains('@') && digest_hex(digest)
        } else {
            false
        };
        if !immutable || !self.image.bytes().all(|byte| (b'!'..=b'~').contains(&byte)) {
            bail!("docker.image must be an immutable local image ID or repository digest");
        }
        if !(1..=256).contains(&self.cpus)
            || !(16..=1_048_576).contains(&self.memory_mb)
            || !(1..=86_400).contains(&self.time_limit_seconds)
        {
            bail!("Docker CPU, memory or time limit is outside the manifest contract");
        }
        Ok(())
    }
}

fn digest_hex(value: &str) -> bool {
    value.len() == 64
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}

pub(super) fn validate_socket_path(path: &Path) -> Result<()> {
    if !path.is_absolute()
        || path.to_str().is_none()
        || path
            .components()
            .any(|component| !matches!(component, Component::RootDir | Component::Normal(_)))
        || path
            .as_os_str()
            .to_string_lossy()
            .chars()
            .any(char::is_control)
    {
        bail!("docker_socket must be an absolute local Unix socket path");
    }
    Ok(())
}

pub(super) fn validate_mount_path(path: &Path) -> Result<()> {
    let value = path
        .to_str()
        .context("Docker workspace path must be UTF-8")?;
    if !path.is_absolute() || value.contains(',') || value.chars().any(char::is_control) {
        bail!("Docker workspace path must be absolute and contain no commas or control characters");
    }
    Ok(())
}

pub(super) async fn prepare(config: &Config) -> Result<()> {
    validate_mount_path(&config.work_root)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::FileTypeExt;
        if unsafe { libc::geteuid() } == 0 {
            bail!("Docker Agent must run as a non-root host user");
        }
        if !tokio::fs::metadata(&config.docker_socket)
            .await
            .context("inspect local Docker socket")?
            .file_type()
            .is_socket()
        {
            bail!("docker_socket is not a local Unix socket");
        }
    }
    #[cfg(not(unix))]
    bail!("Docker execution requires a local Unix host");

    let directory = config.work_root.join(".docker-cli");
    match tokio::fs::create_dir(&directory).await {
        Ok(()) => {}
        Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => {}
        Err(error) => return Err(error.into()),
    }
    let metadata = tokio::fs::symlink_metadata(&directory).await?;
    if !metadata.is_dir()
        || metadata.file_type().is_symlink()
        || tokio::fs::read_dir(&directory)
            .await?
            .next_entry()
            .await?
            .is_some()
    {
        bail!("reserved .docker-cli directory must be an empty ordinary directory");
    }
    let cli = DockerCli::new(config);
    let output = cli.checked(cli.command().args([
        "info", "--format",
        r#"{"os":{{json .OSType}},"memory":{{json .MemoryLimit}},"swap":{{json .SwapLimit}},"cpu":{{json .CPUCfsQuota}},"pids":{{json .PidsLimit}}}"#,
    ]), "docker info").await?;
    validate_capabilities(&output.stdout)?;
    Ok(())
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Capabilities {
    os: String,
    memory: bool,
    swap: bool,
    cpu: bool,
    pids: bool,
}

fn validate_capabilities(bytes: &[u8]) -> Result<()> {
    let capabilities: Capabilities =
        serde_json::from_slice(bytes).context("invalid Docker capability response")?;
    if capabilities.os != "linux"
        || !capabilities.memory
        || !capabilities.swap
        || !capabilities.cpu
        || !capabilities.pids
    {
        bail!(
            "Docker must run locally on Linux with CPU quota, memory, swap and PID limits enabled"
        );
    }
    Ok(())
}

struct DockerCli {
    host: String,
    directory: PathBuf,
    path: OsString,
}

impl DockerCli {
    fn new(config: &Config) -> Self {
        Self {
            host: format!("unix://{}", config.docker_socket.display()),
            directory: config.work_root.join(".docker-cli"),
            // This is the administrator's binary search path, not a container env.
            path: std::env::var_os("PATH").unwrap_or_else(|| "/usr/local/bin:/usr/bin:/bin".into()),
        }
    }

    fn command(&self) -> Command {
        let mut command = Command::new("docker");
        command
            .env_clear()
            .env("PATH", &self.path)
            .env("HOME", &self.directory)
            .env("DOCKER_CONFIG", &self.directory)
            .env("LANG", "C")
            .arg("--config")
            .arg(&self.directory)
            .arg("--host")
            .arg(&self.host);
        command
    }

    async fn checked(&self, command: &mut Command, name: &'static str) -> Result<Output> {
        let (output, truncated) = run_command(command, name, CLI_TIMEOUT, RESPONSE_LIMIT).await?;
        if output.status.code().is_none() || truncated {
            return Err(CommandUncertain(name).into());
        }
        if !output.status.success() {
            bail!(
                "{name} failed: {}",
                String::from_utf8_lossy(&output.stderr).trim()
            );
        }
        Ok(output)
    }

    fn create_command(
        &self,
        config: &Config,
        assignment: &JobAssignment,
        work: &Path,
        manifest: &DockerManifest,
    ) -> Result<Command> {
        validate_mount_path(work)?;
        let memory = format!("{}m", manifest.memory_mb);
        let mut command = self.command();
        command.args([
            "create",
            "--pull",
            "never",
            "--name",
            &container_name(&config.session_id, assignment.job_id),
            "--label",
            &format!("{SESSION_LABEL}={}", config.session_id),
            "--label",
            &format!("{JOB_LABEL}={}", assignment.job_id),
            "--cpus",
            &manifest.cpus.to_string(),
            "--memory",
            &memory,
            "--memory-swap",
            &memory,
            "--pids-limit",
            "512",
            "--network",
            "none",
            "--ipc",
            "none",
            "--read-only",
            "--cap-drop",
            "ALL",
            "--security-opt",
            "no-new-privileges",
            "--restart",
            "no",
            "--no-healthcheck",
            "--log-driver",
            "json-file",
            "--log-opt",
            "max-size=8m",
            "--log-opt",
            "max-file=1",
            "--workdir",
            "/judge",
            "--entrypoint",
            "/bin/sh",
            "--env",
            "PATH=/usr/local/bin:/usr/bin:/bin",
            "--env",
            "HOME=/judge/tmp",
            "--env",
            "TMPDIR=/judge/tmp",
            "--env",
            "LC_ALL=C",
        ]);
        #[cfg(unix)]
        command
            .arg("--user")
            .arg(format!("{}:{}", unsafe { libc::geteuid() }, unsafe {
                libc::getegid()
            }));
        for (source, target, readonly) in [
            ("package", "/judge/package", true),
            ("submission", "/judge/submission", true),
            ("job.json", "/judge/job.json", true),
            ("output", "/judge/output", false),
            ("tmp", "/judge/tmp", false),
        ] {
            command.arg("--mount").arg(format!(
                "type=bind,source={},target={target}{}",
                work.join(source).display(),
                if readonly { ",readonly" } else { "" }
            ));
        }
        command
            .arg("--")
            .arg(&manifest.image)
            .arg("/judge/package/judge.sh");
        Ok(command)
    }

    async fn create(
        &self,
        config: &Config,
        assignment: &JobAssignment,
        work: &Path,
        manifest: &DockerManifest,
    ) -> Result<String> {
        let image = self
            .checked(
                self.command().args([
                    "image",
                    "inspect",
                    "--format",
                    "{{json .Config.Volumes}}",
                    "--",
                    &manifest.image,
                ]),
                "docker image inspect",
            )
            .await?;
        let volumes: serde_json::Value = serde_json::from_slice(&image.stdout)?;
        if !volumes.is_null()
            && !volumes
                .as_object()
                .is_some_and(|volumes| volumes.is_empty())
        {
            bail!("Docker image must not declare implicit writable volumes");
        }
        let name = container_name(&config.session_id, assignment.job_id);
        let output = self
            .checked(
                &mut self.create_command(config, assignment, work, manifest)?,
                "docker create",
            )
            .await
            // A nonzero CLI exit can be a lost response after daemon creation,
            // not a definite rejection. Never discard this Job's bind paths.
            .map_err(|error| error.context(UnknownContainer(name.clone())))?;
        let id = std::str::from_utf8(&output.stdout)
            .unwrap_or_default()
            .trim();
        if !digest_hex(id) {
            return Err(UnknownContainer(name).into());
        }
        Ok(id.to_owned())
    }

    async fn start(&self, id: &str) -> Result<()> {
        self.checked(self.command().args(["start", id]), "docker start")
            .await?;
        Ok(())
    }

    async fn inspect(&self, id: &str, session: &str, job_id: i64) -> Result<ContainerState> {
        let output = self.checked(self.command().args([
            "inspect", "--type", "container", "--format",
            r#"{"Id":{{json .Id}},"State":{{json .State}},"Config":{"Labels":{"soj.agent-session":{{json (index .Config.Labels "soj.agent-session")}},"soj.job":{{json (index .Config.Labels "soj.job")}}}}}"#,
            id,
        ]), "docker inspect").await?;
        parse_inspect(&output.stdout, id, session, job_id)
    }

    async fn kill(&self, id: &str, reason: StopReason) -> Result<StopAttempt> {
        let requested_at = SystemTime::now();
        let (output, truncated) = run_command(
            self.command().args(["kill", "--signal", "KILL", id]),
            "docker kill",
            CLI_TIMEOUT,
            RESPONSE_LIMIT,
        )
        .await?;
        if output.status.code().is_none() || truncated {
            return Err(CommandUncertain("docker kill").into());
        }
        if !output.status.success() {
            // A container may have naturally exited between inspect and kill.
            // Only a later inspect can establish terminal state; a CLI error is
            // never interpreted as cancellation or a stopped container.
            eprintln!(
                "warning: docker kill {id}: {}",
                String::from_utf8_lossy(&output.stderr).trim()
            );
        }
        Ok(StopAttempt {
            reason,
            requested_at,
            acknowledged: output.status.success(),
        })
    }

    async fn logs(&self, id: &str, work: &Path) {
        let outcome = async {
            let (output, truncated) = run_command(
                self.command().args(["logs", "--timestamps", id]),
                "docker logs",
                CLI_TIMEOUT,
                MAX_LOG_COMPONENT_BYTES as usize,
            )
            .await?;
            if !output.status.success() {
                bail!(
                    "docker logs failed: {}",
                    String::from_utf8_lossy(&output.stderr).trim()
                );
            }
            let mut bytes = output.stdout;
            let remaining = (MAX_LOG_COMPONENT_BYTES as usize).saturating_sub(bytes.len());
            bytes.extend_from_slice(&output.stderr[..output.stderr.len().min(remaining)]);
            if truncated || output.stderr.len() > remaining {
                eprintln!("warning: Docker log for {id} truncated to Agent component limit");
            }
            if !bytes.is_empty() {
                tokio::fs::write(work.join("docker.log"), bytes).await?;
            }
            Ok::<_, anyhow::Error>(())
        }
        .await;
        if let Err(error) = outcome {
            eprintln!("warning: cannot collect Docker log for {id}: {error:#}");
        }
    }

    async fn remove(&self, id: &str) -> Result<()> {
        // Deliberately never --force: an unexpected writer must retain its workspace.
        self.checked(self.command().args(["rm", id]), "docker rm")
            .await?;
        Ok(())
    }
}

fn container_name(session: &str, job_id: i64) -> String {
    format!("soj-{}-{job_id}", session.replace('-', ""))
}

#[derive(Debug)]
struct CommandUncertain(&'static str);
impl std::fmt::Display for CommandUncertain {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(formatter, "{} started but its outcome is unknown", self.0)
    }
}
impl std::error::Error for CommandUncertain {}

#[derive(Debug)]
struct UnknownContainer(String);
impl std::fmt::Display for UnknownContainer {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            formatter,
            "Docker execution outcome is unknown for container {}; retaining workspace",
            self.0
        )
    }
}
impl std::error::Error for UnknownContainer {}

async fn read_capped<R: AsyncRead + Unpin>(
    mut reader: R,
    maximum: usize,
) -> std::io::Result<(Vec<u8>, bool)> {
    let mut bytes = Vec::new();
    let mut buffer = [0_u8; 8192];
    let mut truncated = false;
    loop {
        let count = reader.read(&mut buffer).await?;
        if count == 0 {
            return Ok((bytes, truncated));
        }
        let keep = count.min(maximum.saturating_sub(bytes.len()));
        bytes.extend_from_slice(&buffer[..keep]);
        truncated |= keep < count;
        // Drain even after the cap, so an oversized response cannot block wait().
    }
}

async fn run_command(
    command: &mut Command,
    name: &'static str,
    deadline: Duration,
    maximum: usize,
) -> Result<(Output, bool)> {
    let mut child = command
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true)
        .spawn()
        .with_context(|| format!("run {name}"))?;
    let stdout = child.stdout.take().context(CommandUncertain(name))?;
    let stderr = child.stderr.take().context(CommandUncertain(name))?;
    let outcome = timeout(deadline, async {
        tokio::try_join!(
            child.wait(),
            read_capped(stdout, maximum),
            read_capped(stderr, maximum)
        )
    })
    .await;
    match outcome {
        Ok(Ok((status, (stdout, stdout_truncated), (stderr, stderr_truncated)))) => Ok((
            Output {
                status,
                stdout,
                stderr,
            },
            stdout_truncated || stderr_truncated,
        )),
        other => {
            let _ = child.start_kill();
            match timeout(Duration::from_secs(1), child.wait()).await {
                Ok(result) => {
                    result
                        .context("reap failed Docker CLI")
                        .context(CommandUncertain(name))?;
                }
                Err(_) => {
                    return Err(
                        anyhow::anyhow!("Docker CLI could not be reaped after termination")
                            .context(CommandUncertain(name)),
                    )
                }
            }
            let message = match other {
                Ok(Err(error)) => format!("{name} output failed: {error}"),
                Err(_) => format!("{name} exceeded its CLI timeout"),
                Ok(Ok(_)) => unreachable!(),
            };
            Err(anyhow::anyhow!(message).context(CommandUncertain(name)))
        }
    }
}

#[derive(Deserialize)]
struct Inspect {
    #[serde(rename = "Id")]
    id: String,
    #[serde(rename = "State")]
    state: RawState,
    #[serde(rename = "Config")]
    config: InspectConfig,
}
#[derive(Deserialize)]
struct InspectConfig {
    #[serde(rename = "Labels")]
    labels: HashMap<String, String>,
}
#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
struct RawState {
    status: String,
    running: bool,
    paused: bool,
    restarting: bool,
    #[serde(rename = "OOMKilled")]
    oom_killed: bool,
    exit_code: i32,
    error: String,
    started_at: String,
    finished_at: String,
}

#[derive(Clone, Debug)]
struct ContainerState {
    phase: ContainerPhase,
    oom_killed: bool,
    exit_code: i32,
    error: String,
    finished_at: Option<SystemTime>,
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum ContainerPhase {
    Created,
    Running,
    Exited,
    Dead,
}

fn docker_timestamp(value: &str) -> Result<Option<SystemTime>> {
    if value == "0001-01-01T00:00:00Z" || value == "0001-01-01T00:00:00.000000000Z" {
        return Ok(None);
    }
    let timestamp: prost_types::Timestamp =
        value.parse().context("invalid Docker state timestamp")?;
    let timestamp: SystemTime = timestamp
        .try_into()
        .context("Docker state timestamp out of range")?;
    if timestamp <= UNIX_EPOCH {
        bail!("invalid Docker execution timestamp");
    }
    Ok(Some(timestamp))
}

fn parse_inspect(bytes: &[u8], id: &str, session: &str, job_id: i64) -> Result<ContainerState> {
    let inspect: Inspect =
        serde_json::from_slice(bytes).context("invalid Docker inspect response")?;
    if inspect.id != id
        || !digest_hex(&inspect.id)
        || inspect.config.labels.get(SESSION_LABEL).map(String::as_str) != Some(session)
        || inspect.config.labels.get(JOB_LABEL).map(String::as_str)
            != Some(job_id.to_string().as_str())
    {
        bail!("Docker inspect identity or ownership labels do not match this Job");
    }
    let raw = inspect.state;
    if raw.paused || raw.restarting || !(0..=255).contains(&raw.exit_code) {
        bail!("Docker container entered an unsupported execution state");
    }
    let phase = match (raw.status.as_str(), raw.running) {
        ("created", false) => ContainerPhase::Created,
        ("running", true) => ContainerPhase::Running,
        ("exited", false) => ContainerPhase::Exited,
        ("dead", false) => ContainerPhase::Dead,
        _ => bail!("Docker container has an unknown or inconsistent state"),
    };
    let started_at = docker_timestamp(&raw.started_at)?;
    let finished_at = docker_timestamp(&raw.finished_at)?;
    if phase != ContainerPhase::Created && started_at.is_none()
        || matches!(phase, ContainerPhase::Exited | ContainerPhase::Dead)
            && (finished_at.is_none() || finished_at < started_at)
        || phase == ContainerPhase::Running && finished_at.is_some()
    {
        bail!("Docker container has inconsistent execution timestamps");
    }
    Ok(ContainerState {
        phase,
        oom_killed: raw.oom_killed,
        exit_code: raw.exit_code,
        error: raw.error,
        finished_at,
    })
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum StopReason {
    Cancel,
    Timeout,
    Workspace,
}
#[derive(Clone, Copy, Debug)]
struct StopAttempt {
    reason: StopReason,
    requested_at: SystemTime,
    acknowledged: bool,
}

fn terminal_state(
    state: &ContainerState,
    deadline: SystemTime,
    stop: Option<StopAttempt>,
) -> Result<ExecutionTerminalState> {
    if !matches!(state.phase, ContainerPhase::Exited | ContainerPhase::Dead) {
        bail!("Docker execution is not confirmed terminal");
    }
    if state.phase == ContainerPhase::Dead || !state.error.is_empty() {
        bail!(
            "Docker reported an execution infrastructure failure: {}",
            state.error
        );
    }
    if state.oom_killed {
        return Ok(ExecutionTerminalState::OutOfMemory);
    }
    let finished = state
        .finished_at
        .context("terminal Docker execution has no finish time")?;
    let delivered = stop.filter(|stop| {
        stop.acknowledged && state.exit_code == 137 && finished >= stop.requested_at
    });
    if finished >= deadline || delivered.is_some_and(|stop| stop.reason == StopReason::Timeout) {
        return Ok(ExecutionTerminalState::Timeout);
    }
    if delivered.is_some_and(|stop| stop.reason == StopReason::Cancel) {
        return Ok(ExecutionTerminalState::Cancelled);
    }
    Ok(if state.exit_code == 0 {
        ExecutionTerminalState::Completed
    } else {
        ExecutionTerminalState::Failed
    })
}

async fn monitor(
    cli: &Arc<DockerCli>,
    config: &Config,
    control: &mut Control,
    assignment: &JobAssignment,
    work: &Path,
    id: &str,
    status: watch::Receiver<AgentStatus>,
    waiting: &AtomicBool,
    progress: &mut JobProgress,
    limit: Duration,
) -> Result<(
    ContainerState,
    Option<StopAttempt>,
    Option<String>,
    SystemTime,
)> {
    let cancellation = wait_for_cancellation(status, assignment.job_id);
    tokio::pin!(cancellation);
    let mut cancellation_requested = false;
    let mut limit_requested = false;
    // Include Docker start latency in the wall-clock bound, not image loading or staging.
    let deadline = tokio::time::Instant::now() + limit;
    let wall_deadline = SystemTime::now() + limit;
    let execution_id: Arc<str> = Arc::from(id);
    let session_id: Arc<str> = Arc::from(config.session_id.as_str());
    let mut next_stop_attempt = tokio::time::Instant::now();
    let mut stop_deadline = None;
    let mut confirmed_stop = None;
    let mut workspace_error = None;
    let mut terminal: Option<ContainerState> = None;
    let mut checks = WorkspaceChecks::new(work);
    let mut checks_open = true;
    let mut starting = JoinSet::new();
    let start_cli = Arc::clone(cli);
    let start_id = Arc::clone(&execution_id);
    starting.spawn(async move { start_cli.start(&start_id).await });
    let mut start_done = false;
    let mut inspecting = JoinSet::new();
    let mut stopping = JoinSet::new();
    let mut reporting = JoinSet::new();
    let mut poll = interval(Duration::from_millis(250));
    poll.set_missed_tick_behavior(MissedTickBehavior::Delay);
    loop {
        if start_done && stopping.is_empty() && reporting.is_empty() {
            if let Some(state) = terminal.take() {
                if let Err(error) = checks.finish().await {
                    workspace_error
                        .get_or_insert_with(|| format!("workspace limit violation: {error:#}"));
                }
                return Ok((state, confirmed_stop, workspace_error, wall_deadline));
            }
        }
        let reason = if workspace_error.is_some() {
            Some(StopReason::Workspace)
        } else if cancellation_requested {
            Some(StopReason::Cancel)
        } else if limit_requested {
            Some(StopReason::Timeout)
        } else {
            None
        };
        if let Some(reason) = reason {
            if terminal.is_none()
                && stopping.is_empty()
                && confirmed_stop.is_none()
                && tokio::time::Instant::now() >= next_stop_attempt
            {
                stop_deadline.get_or_insert(tokio::time::Instant::now() + STOP_CONFIRM_TIMEOUT);
                let stop_cli = Arc::clone(cli);
                let stop_id = Arc::clone(&execution_id);
                stopping.spawn(async move { stop_cli.kill(&stop_id, reason).await });
            }
        }
        tokio::select! {
            biased;
            _ = &mut cancellation, if !cancellation_requested => { cancellation_requested = true; }
            _ = tokio::time::sleep_until(deadline), if !limit_requested => { limit_requested = true; }
            _ = tokio::time::sleep_until(stop_deadline.unwrap_or(deadline)), if stop_deadline.is_some() => {
                bail!("Docker stop could not be confirmed before its deadline; retaining container {id} and workspace");
            }
            result = starting.join_next(), if !start_done => {
                result.context("Docker start task disappeared")??
                    .with_context(|| UnknownContainer(container_name(&config.session_id, assignment.job_id)))?;
                start_done = true;
            }
            result = stopping.join_next(), if !stopping.is_empty() => {
                let attempt = result.context("Docker stop task disappeared")???;
                next_stop_attempt = tokio::time::Instant::now() + SCHEDULER_POLL;
                if attempt.acknowledged { confirmed_stop = Some(attempt); }
            }
            result = reporting.join_next(), if !reporting.is_empty() => {
                let response = result.context("Docker running report task disappeared")???;
                progress.acknowledge(JobState::Running, response, true);
                if progress.state as i32 >= JobState::Running as i32 {
                    waiting.store(false, Ordering::Release);
                }
            }
            result = checks.next(), if checks_open => {
                checks_open = checks.errors.has_changed().is_ok();
                if let Err(error) = result {
                    workspace_error.get_or_insert_with(|| format!("{error:#}"));
                }
            }
            result = inspecting.join_next(), if !inspecting.is_empty() => {
                let state: ContainerState = result.context("Docker inspect task disappeared")???;
                match state.phase {
                    ContainerPhase::Created => {}
                    ContainerPhase::Running => {
                        if progress.needs_report(JobState::Running, true) && reporting.is_empty() {
                            let mut report_control = control.clone();
                            let report_id = Arc::clone(&execution_id);
                            let session = Arc::clone(&session_id);
                            let job_id = assignment.job_id;
                            reporting.spawn(async move {
                                report_control.report(session.as_ref(), job_id, JobState::Running, Some(report_id.as_ref())).await
                            });
                        }
                    }
                    ContainerPhase::Exited | ContainerPhase::Dead => { terminal = Some(state); }
                }
            }
            _ = poll.tick() => {
                if terminal.is_none() && inspecting.is_empty() {
                    let inspect_cli = Arc::clone(cli);
                    let inspect_id = Arc::clone(&execution_id);
                    let session = Arc::clone(&session_id);
                    let job_id = assignment.job_id;
                    inspecting.spawn(async move { inspect_cli.inspect(&inspect_id, &session, job_id).await });
                }
            }
        }
    }
}

pub(super) async fn execute(
    config: &Config,
    control: &mut Control,
    assignment: &JobAssignment,
    work: &Path,
    manifest: &DockerManifest,
    status: watch::Receiver<AgentStatus>,
    waiting: &AtomicBool,
) -> Result<()> {
    let cli = Arc::new(DockerCli::new(config));
    let id = match cli.create(config, assignment, work, manifest).await {
        Ok(id) => id,
        Err(error) if error.downcast_ref::<UnknownContainer>().is_some() => return Err(error),
        Err(error) => {
            control
                .fail(
                    &config.session_id,
                    assignment.job_id,
                    &format!("cannot create Docker execution: {error:#}"),
                )
                .await?;
            return Ok(());
        }
    };
    let submitted = control
        .report(
            &config.session_id,
            assignment.job_id,
            JobState::Submitted,
            Some(&id),
        )
        .await?;
    if status.borrow().cancellations.contains(&assignment.job_id) {
        // create does not run a process, so it is safe to cancel before start.
        control
            .cancel_job(&config.session_id, assignment.job_id)
            .await?;
        cli.remove(&id).await?;
        return Ok(());
    }
    let mut progress = JobProgress {
        state: submitted,
        execution_id_recorded: true,
    };
    let limit = Duration::from_secs(manifest.time_limit_seconds);
    let (state, stop, workspace_error, deadline) = monitor(
        &cli,
        config,
        control,
        assignment,
        work,
        &id,
        status,
        waiting,
        &mut progress,
        limit,
    )
    .await?;
    cli.logs(&id, work).await;
    if let Some(message) = workspace_error {
        control
            .fail(&config.session_id, assignment.job_id, &message)
            .await?;
    } else {
        match terminal_state(&state, deadline, stop) {
            Ok(terminal) => {
                finish_execution(
                    config,
                    control,
                    assignment.job_id,
                    work,
                    &id,
                    terminal,
                    waiting,
                    &mut progress,
                )
                .await?
            }
            Err(error) => {
                control
                    .fail(
                        &config.session_id,
                        assignment.job_id,
                        &format!("Docker execution failed: {error:#}"),
                    )
                    .await?;
            }
        }
    }
    // A failed/uncertain terminal RPC returns above, retaining both container and
    // files. Only confirmed-terminal executions with known RPC outcome reach rm.
    cli.remove(&id).await?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::tests::proto_value;
    use serde_json::json;

    fn manifest_json() -> serde_json::Value {
        json!({"schemaVersion":1,"problemId":1,"docker":{
            "image":format!("sha256:{}", "a".repeat(64)),
            "cpus":1,"memoryMb":128,"timeLimitSeconds":10
        }})
    }

    fn parse_manifest(value: serde_json::Value) -> Result<Manifest> {
        let manifest: Manifest = serde_json::from_value(value)?;
        manifest.validate()?;
        Ok(manifest)
    }

    #[test]
    fn docker_manifest_requires_exact_nonnull_fields_and_bounded_integer_limits() {
        let baseline = manifest_json();
        assert_eq!(
            parse_manifest(baseline.clone()).unwrap().executor(),
            Executor::Docker
        );
        assert_eq!(
            parse_manifest(json!({"schemaVersion":1,"problemId":1}))
                .unwrap()
                .executor(),
            Executor::Slurm
        );
        for field in ["image", "cpus", "memoryMb", "timeLimitSeconds"] {
            let mut value = baseline.clone();
            value["docker"].as_object_mut().unwrap().remove(field);
            assert!(parse_manifest(value).is_err(), "missing {field}");
        }
        for (field, value) in [
            ("cpus", json!(0)),
            ("cpus", json!(257)),
            ("cpus", json!(1.5)),
            ("cpus", json!("1")),
            ("memoryMb", json!(15)),
            ("memoryMb", json!(1_048_577)),
            ("timeLimitSeconds", json!(0)),
            ("timeLimitSeconds", json!(86_401)),
            ("network", json!("host")),
            ("memoryMB", json!(128)),
        ] {
            let mut invalid = baseline.clone();
            invalid["docker"][field] = value;
            assert!(parse_manifest(invalid).is_err(), "invalid {field}");
        }
        for invalid in [json!(null), json!([]), json!(false)] {
            let mut value = baseline.clone();
            value["docker"] = invalid;
            assert!(parse_manifest(value).is_err());
        }
        for (cpus, memory, seconds) in [(1, 16, 1), (256, 1_048_576, 86_400)] {
            let mut value = baseline.clone();
            value["docker"]["cpus"] = json!(cpus);
            value["docker"]["memoryMb"] = json!(memory);
            value["docker"]["timeLimitSeconds"] = json!(seconds);
            parse_manifest(value).unwrap();
        }
        let duplicate = format!(
            r#"{{"schemaVersion":1,"problemId":1,"docker":{{"image":"sha256:{}","cpus":1,"cpus":2,"memoryMb":128,"timeLimitSeconds":10}}}}"#,
            "a".repeat(64)
        );
        assert!(serde_json::from_str::<Manifest>(&duplicate).is_err());
    }

    #[test]
    fn only_immutable_whitespace_free_lowercase_image_digests_are_allowed() {
        let baseline = manifest_json();
        for image in [
            format!("sha256:{}", "a".repeat(64)),
            format!("registry.local:5000/judge@sha256:{}", "0".repeat(64)),
        ] {
            let mut value = baseline.clone();
            value["docker"]["image"] = json!(image);
            parse_manifest(value).unwrap();
        }
        for image in [
            "python:latest".to_owned(),
            "".to_owned(),
            format!("sha256:{}", "A".repeat(64)),
            format!("sha256:{}", "a".repeat(63)),
            format!("@sha256:{}", "a".repeat(64)),
            format!("repo @sha256:{}", "a".repeat(64)),
            format!("repo\n@sha256:{}", "a".repeat(64)),
            format!("r\u{00e9}po@sha256:{}", "a".repeat(64)),
            format!("repo\u{007f}@sha256:{}", "a".repeat(64)),
            format!("repo\u{001b}@sha256:{}", "a".repeat(64)),
            format!("repo\0@sha256:{}", "a".repeat(64)),
            format!("repo@extra@sha256:{}", "a".repeat(64)),
        ] {
            let mut value = baseline.clone();
            value["docker"]["image"] = json!(image);
            assert!(parse_manifest(value).is_err());
        }
    }

    #[tokio::test]
    async fn downloaded_manifest_must_match_all_assignment_limits_and_selected_script() {
        let value = manifest_json();
        let prost_types::value::Kind::StructValue(snapshot) =
            proto_value(value.clone()).kind.unwrap()
        else {
            unreachable!()
        };
        let mut assignment = JobAssignment {
            executor: AgentExecutor::Docker as i32,
            manifest: Some(snapshot),
            ..JobAssignment::default()
        };
        let expected = assignment_manifest(&assignment).unwrap();
        assignment.executor = AgentExecutor::Slurm as i32;
        assert!(assignment_manifest(&assignment).is_err());
        let package = std::env::temp_dir().join(format!("soj-docker-package-{}", Uuid::new_v4()));
        tokio::fs::create_dir(&package).await.unwrap();
        tokio::fs::write(
            package.join("manifest.json"),
            serde_json::to_vec(&value).unwrap(),
        )
        .await
        .unwrap();
        tokio::fs::write(package.join("judge.sbatch"), b"#!/bin/sh")
            .await
            .unwrap();
        assert!(validate_package(&package, &expected).await.is_err());
        tokio::fs::write(package.join("judge.sh"), b"#!/bin/sh")
            .await
            .unwrap();
        validate_package(&package, &expected).await.unwrap();
        for (field, replacement) in [
            ("image", json!(format!("sha256:{}", "b".repeat(64)))),
            ("cpus", json!(2)),
            ("memoryMb", json!(256)),
            ("timeLimitSeconds", json!(20)),
        ] {
            let mut mismatch = value.clone();
            mismatch["docker"][field] = replacement;
            tokio::fs::write(
                package.join("manifest.json"),
                serde_json::to_vec(&mismatch).unwrap(),
            )
            .await
            .unwrap();
            assert!(
                validate_package(&package, &expected).await.is_err(),
                "snapshot mismatch {field}"
            );
        }
        tokio::fs::remove_dir_all(package).await.unwrap();
    }

    fn inspect_json() -> serde_json::Value {
        json!({"Id":"a".repeat(64),"Config":{"Labels":{"soj.agent-session":"session","soj.job":"42"}},
            "State":{"Status":"exited","Running":false,"Paused":false,"Restarting":false,"OOMKilled":false,
                "ExitCode":0,"Error":"","StartedAt":"2026-11-05T00:00:00Z","FinishedAt":"2026-11-05T00:00:01Z"}})
    }

    fn inspect(value: serde_json::Value) -> Result<ContainerState> {
        parse_inspect(
            &serde_json::to_vec(&value).unwrap(),
            &"a".repeat(64),
            "session",
            42,
        )
    }

    #[test]
    fn inspect_requires_owned_container_consistent_flags_and_terminal_timestamps() {
        assert_eq!(
            inspect(inspect_json()).unwrap().phase,
            ContainerPhase::Exited
        );
        let mut running = inspect_json();
        running["State"]["Status"] = json!("running");
        running["State"]["Running"] = json!(true);
        running["State"]["FinishedAt"] = json!("0001-01-01T00:00:00Z");
        assert_eq!(inspect(running).unwrap().phase, ContainerPhase::Running);
        for (field, value) in [
            ("Running", json!(true)),
            ("Paused", json!(true)),
            ("Restarting", json!(true)),
            ("Status", json!("restarting")),
            ("Status", json!("unknown")),
            ("FinishedAt", json!("0001-01-01T00:00:00Z")),
            ("FinishedAt", json!("not-a-date")),
            ("FinishedAt", json!("2026-11-04T23:59:59Z")),
            ("ExitCode", json!(-1)),
        ] {
            let mut value_json = inspect_json();
            value_json["State"][field] = value;
            assert!(inspect(value_json).is_err(), "invalid inspect {field}");
        }
        let mut wrong_owner = inspect_json();
        wrong_owner["Config"]["Labels"][SESSION_LABEL] = json!("other");
        assert!(inspect(wrong_owner).is_err());
        let mut wrong_job = inspect_json();
        wrong_job["Config"]["Labels"][JOB_LABEL] = json!("43");
        assert!(inspect(wrong_job).is_err());
        let mut wrong_id = inspect_json();
        wrong_id["Id"] = json!("b".repeat(64));
        assert!(inspect(wrong_id).is_err());
        let mut missing = inspect_json();
        missing["State"]
            .as_object_mut()
            .unwrap()
            .remove("OOMKilled");
        assert!(inspect(missing).is_err());
    }

    #[test]
    fn terminal_normalization_respects_oom_wall_clock_and_confirmed_cancellation_races() {
        let mut state = inspect(inspect_json()).unwrap();
        let started = docker_timestamp(inspect_json()["State"]["StartedAt"].as_str().unwrap())
            .unwrap()
            .unwrap();
        let limit = Duration::from_secs(10);
        let deadline = started + limit;
        let cancel = StopAttempt {
            reason: StopReason::Cancel,
            requested_at: started + Duration::from_millis(500),
            acknowledged: true,
        };
        assert_eq!(
            terminal_state(&state, deadline, Some(cancel)).unwrap(),
            ExecutionTerminalState::Completed
        );
        state.exit_code = 137;
        assert_eq!(
            terminal_state(&state, deadline, None).unwrap(),
            ExecutionTerminalState::Failed
        );
        assert_eq!(
            terminal_state(
                &state,
                deadline,
                Some(StopAttempt {
                    acknowledged: false,
                    ..cancel
                })
            )
            .unwrap(),
            ExecutionTerminalState::Failed
        );
        assert_eq!(
            terminal_state(
                &state,
                deadline,
                Some(StopAttempt {
                    requested_at: started + Duration::from_secs(2),
                    ..cancel
                })
            )
            .unwrap(),
            ExecutionTerminalState::Failed
        );
        assert_eq!(
            terminal_state(&state, deadline, Some(cancel)).unwrap(),
            ExecutionTerminalState::Cancelled
        );
        assert_eq!(
            terminal_state(
                &state,
                deadline,
                Some(StopAttempt {
                    reason: StopReason::Timeout,
                    ..cancel
                })
            )
            .unwrap(),
            ExecutionTerminalState::Timeout
        );
        state.finished_at = Some(started + limit - Duration::from_nanos(1));
        state.exit_code = 0;
        assert_eq!(
            terminal_state(&state, deadline, None).unwrap(),
            ExecutionTerminalState::Completed
        );
        state.finished_at = Some(started + limit);
        assert_eq!(
            terminal_state(&state, deadline, Some(cancel)).unwrap(),
            ExecutionTerminalState::Timeout
        );
        state.oom_killed = true;
        assert_eq!(
            terminal_state(&state, deadline, Some(cancel)).unwrap(),
            ExecutionTerminalState::OutOfMemory
        );
        state.error = "runtime failure".into();
        assert!(terminal_state(&state, deadline, None).is_err());
        state.error.clear();
        state.phase = ContainerPhase::Dead;
        assert!(terminal_state(&state, deadline, None).is_err());
        state.phase = ContainerPhase::Running;
        assert!(terminal_state(&state, deadline, Some(cancel)).is_err());
    }

    #[test]
    fn startup_latency_cannot_be_lost_when_natural_exit_wins_the_timeout_kill() {
        let mut raw = inspect_json();
        let requested = docker_timestamp(raw["State"]["StartedAt"].as_str().unwrap())
            .unwrap()
            .unwrap();
        let deadline = requested + Duration::from_secs(10);
        raw["State"]["StartedAt"] = json!("2026-11-05T00:00:02Z");
        raw["State"]["FinishedAt"] = json!("2026-11-05T00:00:11Z");
        let state = inspect(raw).unwrap();
        assert_eq!(
            terminal_state(&state, deadline, None).unwrap(),
            ExecutionTerminalState::Timeout
        );
    }

    #[test]
    fn remote_sockets_unsafe_mount_paths_and_unavailable_limits_are_rejected() {
        for path in [
            "tcp://daemon:2375",
            "docker.sock",
            "/var/run/../docker.sock",
        ] {
            assert!(validate_socket_path(Path::new(path)).is_err());
        }
        for path in ["relative", "/work,readonly", "/work\n"] {
            assert!(validate_mount_path(Path::new(path)).is_err());
        }
        let capabilities = json!({"os":"linux","memory":true,"swap":true,"cpu":true,"pids":true});
        validate_capabilities(&serde_json::to_vec(&capabilities).unwrap()).unwrap();
        for field in ["memory", "swap", "cpu", "pids"] {
            let mut unsupported = capabilities.clone();
            unsupported[field] = json!(false);
            assert!(validate_capabilities(&serde_json::to_vec(&unsupported).unwrap()).is_err());
        }
        let mut remote_os = capabilities;
        remote_os["os"] = json!("windows");
        assert!(validate_capabilities(&serde_json::to_vec(&remote_os).unwrap()).is_err());
        assert!(validate_capabilities(b"{}").is_err());
    }

    #[tokio::test]
    async fn docker_claims_stay_serial_after_running_and_through_drain() {
        use tokio::sync::oneshot;
        let mut jobs = JobTasks {
            serial: true,
            ..JobTasks::default()
        };
        let (released, release) = oneshot::channel();
        let (finish, finished) = oneshot::channel();
        jobs.spawn(42, |waiting| async move {
            waiting.store(false, Ordering::Release);
            released.send(()).unwrap();
            finished.await?;
            Ok(())
        })
        .unwrap();
        release.await.unwrap();
        assert!(!jobs.can_claim(false, AgentState::Online));
        assert!(!jobs.can_claim(false, AgentState::Draining));
        finish.send(()).unwrap();
        jobs.join_next().await.unwrap();
        assert!(jobs.can_claim(false, AgentState::Online));
        assert!(!jobs.can_claim(false, AgentState::Draining));
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn blocked_docker_cli_remains_cancellable_and_timeout_is_uncertain_not_terminal() {
        let work = std::env::temp_dir().join(format!("soj-docker-cli-timeout-{}", Uuid::new_v4()));
        tokio::fs::create_dir(&work).await.unwrap();
        let pid_file = work.join("cli.pid");
        let (sender, receiver) = watch::channel(AgentStatus {
            state: AgentState::Online,
            cancellations: Arc::new(HashSet::new()),
        });
        let mut command = Command::new("/bin/sh");
        command
            .args([
                "-c",
                "printf '%s' \"$$\" > \"$1\"; exec sleep 60",
                "soj-docker-cli",
            ])
            .arg(&pid_file);
        let cli = tokio::spawn(async move {
            run_command(
                &mut command,
                "docker kill",
                Duration::from_millis(250),
                4096,
            )
            .await
        });
        sender
            .send(AgentStatus {
                state: AgentState::Online,
                cancellations: Arc::new(HashSet::from([42])),
            })
            .unwrap();
        timeout(
            Duration::from_millis(50),
            wait_for_cancellation(receiver, 42),
        )
        .await
        .unwrap();
        assert!(!cli.is_finished());
        let error = cli.await.unwrap().unwrap_err();
        assert!(error.downcast_ref::<CommandUncertain>().is_some());
        assert!(error.downcast_ref::<SlurmCommandTimeout>().is_none());
        let pid = tokio::fs::read_to_string(pid_file).await.unwrap();
        assert!(!PathBuf::from(format!("/proc/{pid}")).exists());
        assert!(work.is_dir());
        tokio::fs::remove_dir_all(work).await.unwrap();
    }

    #[tokio::test]
    async fn cli_output_is_bounded_but_drained_without_deadlocking_the_child() {
        let mut command = Command::new("/bin/sh");
        command.args(["-c", "printf '123456789'; printf 'abcdefghi' >&2"]);
        let (output, truncated) =
            run_command(&mut command, "docker inspect", Duration::from_secs(1), 4)
                .await
                .unwrap();
        assert!(output.status.success());
        assert!(truncated);
        assert_eq!(output.stdout, b"1234");
        assert_eq!(output.stderr, b"abcd");
    }
}
