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

        self.logger.info(f"Uploading ergate binary from {binary_path}")

        # Install ca-certificates for TLS (fixes x509 errors on minimal containers).
        await environment.exec(
            "apt-get update -qq && apt-get install -y -qq ca-certificates 2>/dev/null || true",
            timeout_sec=60,
        )

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

        self.logger.info(
            f"Running ergate (model={self.extra_env.get('ERGATE_MODEL', 'default')}, "
            f"max_turns={self.max_turns})"
        )
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
