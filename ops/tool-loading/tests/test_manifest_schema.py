import copy
import json
from pathlib import Path
import unittest

from jsonschema import Draft202012Validator


ROOT = Path(__file__).resolve().parents[1]


def fixture_manifest():
    scope = {
        "gateway_revision": "a" * 40,
        "policy_generation": "b" * 64,
        "client": "codex",
        "client_version": "fixture-1",
        "client_revision": "c" * 64,
        "client_mode": "tool-search",
        "source_format": "codex",
        "provider": "fixture",
        "model": "fixture-model",
        "upstream_format": "openai-response",
        "endpoint_id": "ep_" + "d" * 32,
        "transport": "http-sse",
        "catalog_hash": "e" * 64,
        "fixture_hash": "f" * 64,
        "permission_hash": "0" * 64,
        "region_id": "fixture-region",
    }
    return {
        "schema_version": 1,
        "run_id": "fixture-run-1",
        "scope": scope,
        "profile": {
            "client_search": "bridge",
            "activation": "cumulative-budgeted",
            "additional_tool_positions": "unknown",
            "transports": {
                "http-buffered": "declared", "http-sse": "declared",
                "websocket-replay": "unknown", "websocket-duplex": "unknown",
            },
            "evidence": "unknown",
        },
        "evidence": [],
    }


class ManifestSchemaTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.schema = json.loads((ROOT / "manifest.schema.json").read_text())
        Draft202012Validator.check_schema(cls.schema)
        cls.validator = Draft202012Validator(cls.schema)

    def assert_invalid(self, value):
        self.assertTrue(list(self.validator.iter_errors(value)))

    def test_valid_policy_with_no_live_evidence(self):
        self.validator.validate(fixture_manifest())

    def test_every_scope_dimension_is_required(self):
        for key in fixture_manifest()["scope"]:
            with self.subTest(key=key):
                value = fixture_manifest()
                del value["scope"][key]
                self.assert_invalid(value)

    def test_unknown_scope_is_explicit_and_not_zero_or_null(self):
        value = fixture_manifest()
        for key in value["scope"]:
            value["scope"][key] = "unknown"
        self.validator.validate(value)
        for missing in (None, 0, False, ""):
            with self.subTest(value=missing):
                invalid = copy.deepcopy(value)
                invalid["scope"]["client_version"] = missing
                self.assert_invalid(invalid)

    def test_addresses_credentials_and_declarations_are_not_manifest_fields(self):
        for container, key in (("root", "api_key"), ("scope", "base_url"), ("profile", "tools")):
            with self.subTest(container=container):
                value = fixture_manifest()
                target = value if container == "root" else value[container]
                target[key] = "fixture-private-data"
                self.assert_invalid(value)
        value = fixture_manifest()
        value["scope"]["endpoint_id"] = "https://fixture.invalid"
        self.assert_invalid(value)

    def test_declared_policy_cannot_become_live_certificate(self):
        value = fixture_manifest()
        value["profile"]["evidence"] = "bounded-live"
        self.assert_invalid(value)

    def test_unknown_identifier_is_canonical(self):
        for label in ("UNKNOWN", "Unknown", "unKNOWN"):
            value = fixture_manifest()
            value["scope"]["client_version"] = label
            self.assert_invalid(value)

    def test_verified_evidence_requires_known_scope_and_artifact_digest(self):
        value = fixture_manifest()
        record = {"scope": copy.deepcopy(value["scope"]), "status": "bounded-live", "artifact_id": "fixture-artifact", "artifact_sha256": "1" * 64}
        value["evidence"] = [record]
        self.validator.validate(value)
        for key in record["scope"]:
            with self.subTest(key=key):
                invalid = copy.deepcopy(value)
                invalid["evidence"][0]["scope"][key] = "unknown"
                self.assert_invalid(invalid)
        record["artifact_sha256"] = "unknown"
        self.assert_invalid(value)

    def test_transformation_is_content_free(self):
        value = fixture_manifest()
        value["transformation"] = {"positions": {"declaration_indices": [0, 2], "interleaved": True, "same_identity_changed": False}, "position_preserved": False, "losses": ["additional_tools_position_lift"]}
        self.validator.validate(value)
        value["transformation"]["positions"]["schema"] = {"private": "fixture"}
        self.assert_invalid(value)


if __name__ == "__main__":
    unittest.main()
