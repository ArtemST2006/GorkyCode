"""Environment-backed settings shared by HTTP and model layers."""

from typing import Literal

from pydantic import AliasChoices, Field, SecretStr, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_file=".env", extra="ignore", case_sensitive=False, populate_by_name=True
    )

    llm_provider: Literal["cloudru"] = "cloudru"
    cloudru_api_key: SecretStr | None = Field(
        default=None, validation_alias=AliasChoices("CLOUDRU_API_KEY", "API_KEY")
    )
    cloudru_base_url: str = "https://foundation-models.api.cloud.ru/v1"
    cloudru_model: str = Field(default="ai-sage/GigaChat3-10B-A1.8B", min_length=1)
    cloudru_force_ipv4: bool = False
    cloudru_timeout_seconds: float = Field(default=60.0, gt=0, le=300)
    cloudru_max_retries: int = Field(default=0, ge=0, le=3)
    cloudru_max_tokens: int = Field(
        default=2500, ge=128, le=32768,
        validation_alias=AliasChoices(
            "CLOUDRU_MAX_TOKENS", "CLOUDRU_MAX_COMPLETION_TOKENS",
            "cloudru_max_completion_tokens",
        ),
    )

    @field_validator("cloudru_base_url")
    @classmethod
    def validate_cloudru_base_url(cls, value: str) -> str:
        from urllib.parse import urlsplit

        parsed = urlsplit(value)
        if (
            parsed.scheme != "https"
            or not parsed.hostname
            or parsed.username
            or parsed.password
            or parsed.query
            or parsed.fragment
        ):
            raise ValueError("Cloud.ru base URL must be HTTPS without credentials/query/fragment")
        return value.rstrip("/")

    job_timeout_seconds: float = Field(default=150.0, gt=0, le=600)
    security_guard_mode: Literal["enforce", "observe"] = "enforce"
    security_expose_docs: bool = False
    security_guard_model: str = Field(default="", max_length=256)
    security_guard_timeout_seconds: float = Field(default=15.0, gt=0, le=120)
    security_guard_max_tokens: int = Field(default=256, ge=128, le=2048)
    security_guard_max_concurrency: int = Field(default=4, ge=1, le=64)
    security_job_max_body_bytes: int = Field(default=262144, ge=1024, le=4194304)
    security_respond_max_body_bytes: int = Field(default=524288, ge=1024, le=4194304)
    security_user_max_chars: int = Field(default=8192, ge=1, le=131072)
    security_history_message_max_chars: int = Field(default=8192, ge=1, le=131072)
    security_context_max_chars: int = Field(default=32768, ge=1024, le=524288)
    embedding_enabled: bool = True
    embedding_model_path: str = "models/multilingual-e5-small"
    embedding_num_threads: int = Field(default=2, ge=1, le=64)
    embedding_max_workers: int = Field(default=1, ge=1, le=16)
    embedding_timeout_seconds: float = Field(default=10.0, gt=0, le=300)
    log_level: Literal["DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"] = "INFO"
    port: int = Field(default=8090, ge=1, le=65535)
