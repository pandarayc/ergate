"""
Ergate agent for Harbor / Terminal-Bench.

Usage:
    harbor run -a ergate -m deepseek/deepseek-v4-pro \\
        --agent-import-path tasks.ergate_agent:ErgateAgent \\
        --ae ANTHROPIC_BASE_URL=https://api.deepseek.com/anthropic \\
        --ae ANTHROPIC_AUTH_TOKEN=sk-xxx \\
        -d terminal-bench/terminal-bench-2 \\
        -l 1

To build the required static binary first:
    cd /path/to/ergate && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o tasks/ergate-static ./cmd/ergate/
"""

import asyncio
import logging
import os
import shlex
from pathlib import Path

from harbor.agents.base import BaseAgent
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext

AGENT_BINARY = "ergate-static"

logger = logging.getLogger(__name__)


class ErgateAgent(BaseAgent):
    """Minimal Harbor agent that runs ergate as a static binary inside the
    benchmark container."""

    SUPPORTS_ATIF: bool = False
    SUPPORTS_WINDOWS: bool = False

    def __init__(
        self,
        logs_dir: Path,
        model_name: str | None = None,
        logger: logging.Logger | None = None,
        mcp_servers: list | None = None,
        skills_dir: str | None = None,
        max_turns: int = 30,
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
        # Path to the ergate binary on the HOST (for upload).
        self._agent_binary = agent_binary or self._find_binary()

    @staticmethod
    def name() -> str:
        return "ergate"

    def version(self) -> str | None:
        return "0.1.0"

    @staticmethod
    def _find_binary() -> str:
        """Find the ergate static binary relative to this source file."""
        here = Path(__file__).resolve().parent
        candidate = here / AGENT_BINARY
        if candidate.exists():
            return str(candidate)
        # Fallback: look in current directory.
        if Path(AGENT_BINARY).exists():
            return AGENT_BINARY
        return AGENT_BINARY  # let it fail with clear error later

    async def setup(self, environment: BaseEnvironment) -> None:
        """Upload the ergate static binary into the container."""
        binary_path = self._agent_binary
        if not Path(binary_path).exists():
            raise FileNotFoundError(
                f"Ergate binary not found at {binary_path}. "
                f"Build it first: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "
                f"go build -o tasks/{AGENT_BINARY} ./cmd/ergate/"
            )

        self.logger.info(f"Uploading ergate binary from {binary_path}")

        # Upload to /usr/local/bin inside the container.
        await environment.upload_file(binary_path, f"/tmp/{AGENT_BINARY}")

        # Make executable.
        result = await environment.exec(
            f"chmod +x /tmp/{AGENT_BINARY} && "
            f"mv /tmp/{AGENT_BINARY} /usr/local/bin/ergate && "
            f"which ergate && ergate --version"
        )
        self.logger.info(f"Ergate setup: {result.stdout}")

    async def run(
        self,
        instruction: str,
        environment: BaseEnvironment,
        context: AgentContext,
    ) -> None:
        """Run ergate against the task instruction."""
        # Build CLI command with proper quoting.
        cmd = "ergate --headless -p " + shlex.quote(instruction)

        # Collect env vars for ergate. Harbor's --agent-env are passed
        # to __init__ as extra_env, but may also appear in os.environ.
        ergate_env = {}
        ergate_keys = (
            "ERGATE_API_PROVIDER", "ERGATE_API_KEY",
            "ERGATE_BASE_URL", "ERGATE_MODEL",
            "ERGATE_MAX_TURNS", "ERGATE_MAX_TOKENS",
        )
        for key in ergate_keys:
            if key in os.environ:
                ergate_env[key] = os.environ[key]
        # extra_env (from --agent-env) overrides os.environ.
        ergate_env.update({k: v for k, v in self.extra_env.items() if k in ergate_keys})

        api_ok = "ERGATE_API_KEY" in ergate_env
        self.logger.info(
            f"Running ergate (api_key={'SET' if api_ok else 'MISSING'}, "
            f"provider={ergate_env.get('ERGATE_API_PROVIDER', 'default')}, "
            f"model={ergate_env.get('ERGATE_MODEL', 'default')})"
        )
        self.logger.info(f"Instruction: {instruction[:100]}...")

        # Collect output as it streams.
        collected: list[str] = []

        async def on_output(text: str, stream: str) -> None:
            if stream == "stdout":
                collected.append(text)
            self.logger.debug(f"[{stream}] {text}")

        try:
            with environment.scoped_output_callback(on_output):
                result = await environment.exec(
                    cmd,
                    env=ergate_env,
                    timeout_sec=600,
                )
        except asyncio.TimeoutError:
            self.logger.warning("Ergate timed out")
            context.metadata = {"error": "timeout", "output": "".join(collected)}
            return

        output = result.stdout if result else "".join(collected)
        self.logger.info(f"Ergate exit={result.return_code if result else '?'}")

        context.metadata = {
            "exit_code": result.return_code if result else -1,
            "output": output[:50000],
        }
