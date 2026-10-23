import pytest
from pydantic import ValidationError

from app.core.config import Settings


def test_settings_preserve_requested_model(monkeypatch):
    monkeypatch.delenv("CLOUDRU_MODEL", raising=False)
    settings = Settings(_env_file=None)
    assert settings.cloudru_model == "ai-sage/GigaChat3-10B-A1.8B"
    assert settings.port == 8090


def test_environment_key_is_secret_and_unknown_env_allowed(monkeypatch):
    monkeypatch.setenv("CLOUDRU_API_KEY", "test-secret")
    monkeypatch.setenv("CLOUDRU_MODEL", "configured-model")
    settings = Settings(_env_file=None, unrelated_setting="ignored")
    assert settings.cloudru_api_key.get_secret_value() == "test-secret"
    assert "test-secret" not in repr(settings)
    assert settings.cloudru_model == "configured-model"


@pytest.mark.parametrize(
    "field,value",
    [
        ("cloudru_timeout_seconds", 0),
        ("job_timeout_seconds", -1),
        ("cloudru_max_retries", 4),
        ("cloudru_max_retries", -1),
        ("port", 65536),
        ("cloudru_max_tokens", 0),
    ],
)
def test_invalid_limits_are_rejected(field, value):
    with pytest.raises(ValidationError):
        Settings(_env_file=None, **{field: value})


def test_ipv4_flag_can_be_enabled_from_environment(monkeypatch):
    monkeypatch.setenv("CLOUDRU_FORCE_IPV4", "true")
    assert Settings(_env_file=None).cloudru_force_ipv4 is True


def test_cloudru_default_selection_and_secret(monkeypatch):
    monkeypatch.setenv("LLM_PROVIDER", "cloudru")
    monkeypatch.setenv("CLOUDRU_API_KEY", "cloudru-test-secret")
    value = Settings(_env_file=None)
    assert value.llm_provider == "cloudru"
    assert value.cloudru_model == "ai-sage/GigaChat3-10B-A1.8B"
    assert value.cloudru_api_key.get_secret_value() == "cloudru-test-secret"
    assert "cloudru-test-secret" not in repr(value)


@pytest.mark.parametrize(
    "base",
    [
        "http://example.com/v1",
        "https://key@example.com/v1",
        "https://example.com/v1?secret=x",
        "https://example.com/v1#x",
    ],
)
def test_cloudru_rejects_unsafe_base_urls(base):
    with pytest.raises(ValidationError):
        Settings(_env_file=None, cloudru_base_url=base)


@pytest.mark.parametrize(
    "values",
    [
        {"llm_provider": "unknown"},
        {"cloudru_max_retries": 4},
        {"cloudru_timeout_seconds": 0},
        {"cloudru_max_completion_tokens": 0},
        {"cloudru_max_tokens": 0},
    ],
)
def test_invalid_cloudru_options(values):
    with pytest.raises(ValidationError):
        Settings(_env_file=None, **values)


def test_provider_example_api_key_alias_and_priority(monkeypatch):
    monkeypatch.delenv("CLOUDRU_API_KEY", raising=False)
    monkeypatch.setenv("API_KEY", "example-secret")
    assert Settings(_env_file=None).cloudru_api_key.get_secret_value() == "example-secret"
    monkeypatch.setenv("CLOUDRU_API_KEY", "provider-secret")
    value = Settings(_env_file=None)
    assert value.cloudru_api_key.get_secret_value() == "provider-secret"
    assert "provider-secret" not in repr(value)


def test_cloudru_defaults_match_example():
    value = Settings(_env_file=None)
    assert value.cloudru_max_tokens == 2500
    assert value.cloudru_timeout_seconds == 60
    assert value.job_timeout_seconds == 150


def test_max_tokens_accepts_existing_environment_name(monkeypatch):
    monkeypatch.delenv("CLOUDRU_MAX_TOKENS", raising=False)
    monkeypatch.setenv("CLOUDRU_MAX_COMPLETION_TOKENS", "1024")
    assert Settings(_env_file=None).cloudru_max_tokens == 1024
    monkeypatch.setenv("CLOUDRU_MAX_TOKENS", "2500")
    assert Settings(_env_file=None).cloudru_max_tokens == 2500
