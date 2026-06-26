"""
Ergate agent for Harbor / Terminal-Bench.

Writes an ergate config file during setup so ergate reads credentials
automatically. Pass credentials via Harbor's --agent-env:

    harbor run \\
        --agent-import-path tasks.ergate_agent:ErgateAgent \\
        --ae ERGATE_API_KEY=sk-xxx \\
        --ae ERGATE_BASE_URL=https://api.deepseek.com/anthropic \\
        --ae ERGATE_MODEL=deepseek-v4-pro \\
        -d terminal-bench/terminal-bench-2 -l 3

Or just forward all existing env vars:

    harbor run \\
        --agent-import-path tasks.ergate_agent:ErgateAgent \\
        --ae ERGATE_API_KEY=$ANTHROPIC_AUTH_TOKEN \\
        --ae ERGATE_BASE_URL=$ANTHROPIC_BASE_URL \\
        --ae ERGATE_MODEL=deepseek-v4-pro \\
        --ae ERGATE_MAX_TURNS=25 \\
        -d terminal-bench/terminal-bench-2 -l 3

Proxy support (for GFW / restricted networks):

    harbor run \\
        --agent-import-path tasks.ergate_agent:ErgateAgent \\
        --ae HTTP_PROXY=http://host.docker.internal:7897 \\
        --ae HTTPS_PROXY=http://host.docker.internal:7897 \\
        --ae NO_PROXY=localhost,127.0.0.1 \\
        ... other env vars \\
        -d terminal-bench/terminal-bench-2 -l 3

Proxy vars (HTTP_PROXY, HTTPS_PROXY, NO_PROXY) are injected into the
container environment so ergate's WebFetch/WebSearch tools can route
HTTP requests through the proxy. Go's net/http reads them automatically.
"""

import asyncio
import logging
import os
import shlex
import textwrap
from pathlib import Path

from harbor.agents.base import BaseAgent
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext

AGENT_BINARY = "ergate-static"
EREGATE_CONFIG_DIR = "/home/agent/.ergate"

# Proxy env var names that are forwarded to the container environment.
_PROXY_ENV_VARS = (
    "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
    "http_proxy", "https_proxy", "no_proxy",
)

logger = logging.getLogger(__name__)


class ErgateAgent(BaseAgent):
    SUPPORTS_ATIF: bool = False
    SUPPORTS_WINDOWS: bool = False

    def __init__(
        self,
        logs_dir: Path,
        model_name: str | None = None,
        logger: logging.Logger | None = None,
        mcp_servers: list | None = None,
        skills_dir: str | None = None,
        max_turns: int = 25,
        agent_binary: str | None = None,
        extra_env: dict | None = None,
        **kwargs,
    ):
        super().__init__(
            logs_dir=logs_dir,
            model_name=model_name,
            logger=logger,
            mcp_servers=mcp_servers,
            skills_dir=skills_dir,
        )
        self.max_turns = max_turns
        self.extra_env = extra_env or {}
        self._agent_binary = agent_binary or self._find_binary()

    @staticmethod
    def name() -> str:
        return "ergate"

    def version(self) -> str | None:
        return "0.1.0"

    @staticmethod
    def _find_binary() -> str:
        here = Path(__file__).resolve().parent
        candidate = here / AGENT_BINARY
        if candidate.exists():
            return str(candidate)
        if Path(AGENT_BINARY).exists():
            return AGENT_BINARY
        return AGENT_BINARY

    async def setup(self, environment: BaseEnvironment) -> None:
        # 1. Upload ergate binary.
        binary_path = self._agent_binary
        if not Path(binary_path).exists():
            raise FileNotFoundError(
                f"Ergate binary not found at {binary_path}. "
                f"Build: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "
                f"go build -o tasks/{AGENT_BINARY} ./cmd/ergate/"
            )

        # Build proxy env for setup commands (apt-get etc.).
        setup_proxy_env: dict[str, str] = {}
        for key in _PROXY_ENV_VARS:
            if key in self.extra_env:
                setup_proxy_env[key] = self.extra_env[key]

        # Inject proxy at container level so ALL processes benefit:
        # - apt-get (via /etc/apt/apt.conf.d)
        # - curl/wget (via /etc/environment)
        # - verifier test.sh (inherits from /etc/environment)
        if setup_proxy_env:
            proxy_url = setup_proxy_env.get("HTTPS_PROXY") or setup_proxy_env.get("https_proxy") or ""
            if proxy_url:
                try:
                    await environment.exec(
                        f"mkdir -p /etc/apt/apt.conf.d && "
                        f"echo 'Acquire::http::Proxy \"{proxy_url}\";' > /etc/apt/apt.conf.d/01proxy && "
                        f"echo 'Acquire::https::Proxy \"{proxy_url}\";' >> /etc/apt/apt.conf.d/01proxy",
                        timeout_sec=10,
                    )
                    self.logger.info(f"Container-level proxy configured: {proxy_url}")
                except Exception as e:
                    self.logger.warning(f"Failed to configure container proxy: {e}")
            # Also set in /etc/environment for non-apt tools
            try:
                env_lines = " ".join(f'echo "{k}={v}" >> /etc/environment' for k, v in setup_proxy_env.items())
                await environment.exec(env_lines, timeout_sec=10)
            except Exception:
                pass

        self.logger.info(f"Uploading ergate binary from {binary_path}")

        # Install ca-certificates for TLS (fixes x509 errors on minimal containers).
        # Some Terminal-Bench images lack them, causing "tls: failed to verify
        # certificate" when ergate calls the LLM API. Check first, install only
        # if needed. Use proxy to speed up apt behind GFW.
        has_certs = False
        try:
            result = await environment.exec(
                "dpkg -l ca-certificates 2>/dev/null | grep -q '^ii' && echo 'INSTALLED' || echo 'MISSING'",
                timeout_sec=10,
            )
            has_certs = "INSTALLED" in (result.stdout or "")
        except Exception:
            pass

        if has_certs:
            self.logger.info("ca-certificates already installed")
        else:
            self.logger.info("ca-certificates missing — installing via apt")
            await environment.exec(
                "apt-get update -o Acquire::http::Timeout=10 -qq "
                "&& apt-get install -y -qq --no-install-recommends ca-certificates "
                "&& update-ca-certificates",
                timeout_sec=120,
                env=setup_proxy_env if setup_proxy_env else None,
            )
            self.logger.info("ca-certificates installed and updated")

        # Warm the apt cache and pre-install packages that ALL verifiers need.
        # This prevents 5+ containers from concurrently downloading 8.7MB of
        # package indexes + curl/pkg through the proxy at test time.
        # curl: used by every test.sh to download uv. Pre-installing makes
        #       verifier's `apt-get install curl` a no-op.
        try:
            await environment.exec(
                "apt-get update -o Acquire::http::Timeout=30 -qq "
                "&& apt-get install -y -qq --no-install-recommends curl ca-certificates 2>/dev/null "
                "&& apt-get clean",
                timeout_sec=180,
                env=setup_proxy_env if setup_proxy_env else None,
            )
            self.logger.info("apt cache warmed + curl pre-installed")
        except Exception:
            self.logger.warning("apt cache warm skipped")

        # Pre-install uv (Python package manager) — many Terminal-Bench
        # verifiers use `uvx` which downloads uv from GitHub on first use.
        # GitHub release assets are blocked by GFW, so pre-installing avoids
        # "SSL_ERROR_SYSCALL" / "failed to download uv" failures in test.sh.
        try:
            await environment.exec(
                "curl -LsSf https://astral.sh/uv/install.sh | sh 2>/dev/null || true",
                timeout_sec=60,
                env=setup_proxy_env if setup_proxy_env else None,
            )
            await environment.exec(
                "cp $HOME/.local/bin/uv /usr/local/bin/ 2>/dev/null; "
                "cp $HOME/.local/bin/uvx /usr/local/bin/ 2>/dev/null; "
                "echo 'uv pre-installed'",
                timeout_sec=10,
            )
        except Exception:
            self.logger.info("uv pre-install skipped")

        await environment.upload_file(binary_path, f"/tmp/{AGENT_BINARY}")
        await environment.exec(
            f"chmod +x /tmp/{AGENT_BINARY} && "
            f"mv /tmp/{AGENT_BINARY} /usr/local/bin/ergate"
        )

        # 2. Build ergate env vars from extra_env (Harbor --ae flags).
        # If not set, ergate reads from its own config/env — no hardcoded defaults.
        ergate_env = dict(self.extra_env)  # copy
        api_key = ergate_env.get("ERGATE_API_KEY", "")
        model = ergate_env.get("ERGATE_MODEL", "deepseek-v4-pro")
        api_provider = ergate_env.get("ERGATE_API_PROVIDER", "anthropic")
        base_url = ergate_env.get("ERGATE_BASE_URL", "https://api.deepseek.com/anthropic")
        compat = ergate_env.get("ERGATE_COMPAT", "anthropic")  # API protocol: anthropic|openai
        max_turns = ergate_env.get("ERGATE_MAX_TURNS", str(self.max_turns))

        self.logger.info(
            f"Config: provider={api_provider} model={model} "
            f"max_turns={max_turns} api_key={'SET' if api_key else 'MISSING'}"
        )

        if not api_key:
            self.logger.warning("ERGATE_API_KEY not set — ergate may fail")

        config_yaml = textwrap.dedent(f"""\
        api_provider: {api_provider}
        api_key: "{api_key}"
        model: "{model}"
        max_turns: {max_turns}
        permission_mode: bypass
        providers:
          {api_provider}:
            compat: {compat}
            base_url: "{base_url}"
            api_key: "{api_key}"
            models:
              "{model}":
                thinking_budget: 4000
        """)

        await environment.exec(f"mkdir -p {EREGATE_CONFIG_DIR}")
        # Write via echo to avoid quoting issues.
        await environment.exec(
            f"cat > {EREGATE_CONFIG_DIR}/config.yaml << 'EOF'\n{config_yaml}EOF"
        )
        self.logger.info(f"Config written to {EREGATE_CONFIG_DIR}/config.yaml")

        # Verify.
        result = await environment.exec("ergate --version")
        self.logger.info(f"Ergate ready: {result.stdout.strip()}")

    async def run(
        self,
        instruction: str,
        environment: BaseEnvironment,
        context: AgentContext,
    ) -> None:
        cmd = (
            "ergate -c " + shlex.quote(EREGATE_CONFIG_DIR + "/config.yaml") +
            " --headless -p " + shlex.quote(instruction)
        )

        # Do NOT pass proxy env vars to ergate process — the Go net/http
        # client would route ALL requests (including LLM API calls) through
        # the proxy, adding latency per turn. Container-level proxy (apt
        # config + /etc/environment) already handles verifier dependencies.
        run_env: dict[str, str] = {}

        self.logger.info(
            f"Running ergate (model={self.extra_env.get('ERGATE_MODEL', 'default')}, "
            f"max_turns={self.max_turns})"
        )
        if run_env:
            self.logger.info(f"Proxy: {run_env.get('HTTP_PROXY', run_env.get('http_proxy', 'none'))}")
        self.logger.info(f"Instruction: {instruction[:120]}...")

        collected: list[str] = []

        async def on_output(text: str, stream: str) -> None:
            if stream == "stdout":
                collected.append(text)
            self.logger.debug(f"[{stream}] {text[:200]}")

        try:
            with environment.scoped_output_callback(on_output):
                result = await environment.exec(
                    cmd,
                    env=run_env if run_env else None,
                    timeout_sec=1200,
                )
        except asyncio.TimeoutError:
            self.logger.warning("Ergate timed out")
            context.metadata = {"error": "timeout", "output": "".join(collected)}
            return

        output = result.stdout if result else "".join(collected)
        self.logger.info(f"Ergate exit={result.return_code if result else '?'}")
        context.metadata = {
            "exit_code": result.return_code if result else -1,
            "output": output[-50000:],
        }
