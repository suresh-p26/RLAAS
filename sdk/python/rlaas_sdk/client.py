from __future__ import annotations

from typing import Any, Dict, List, Optional
import requests

from .types import CheckRequest, Decision, Policy


class RlaasClient:
    def __init__(self, base_url: str, timeout_seconds: float = 5.0) -> None:
        self.base_url = base_url.rstrip("/")
        self.timeout_seconds = timeout_seconds
        self.session = requests.Session()

    def check_limit(self, req: CheckRequest) -> Decision:
        data = self._post("/rlaas/v1/check", req.to_dict())
        return Decision(
            allowed=bool(data.get("allowed", False)),
            action=str(data.get("action", "")),
            reason=str(data.get("reason", "")),
            remaining=int(data.get("remaining", 0) or 0),
            retry_after=str(data.get("retry_after", "")),
        )

    def acquire(self, request: Dict[str, Any]) -> Dict[str, Any]:
        return self._post("/rlaas/v1/acquire", request)

    def release(self, lease_id: str) -> Dict[str, Any]:
        return self._post("/rlaas/v1/release", {"lease_id": lease_id})

    def list_policies(self) -> List[Dict[str, Any]]:
        return self._get("/rlaas/v1/policies")

    def get_policy(self, policy_id: str) -> Dict[str, Any]:
        return self._get(f"/rlaas/v1/policies/{policy_id}")

    def create_policy(self, policy: Policy) -> Dict[str, Any]:
        return self._post("/rlaas/v1/policies", policy.to_dict())

    def update_policy(self, policy_id: str, policy: Policy) -> Dict[str, Any]:
        return self._put(f"/rlaas/v1/policies/{policy_id}", policy.to_dict())

    def delete_policy(self, policy_id: str) -> None:
        self._delete(f"/rlaas/v1/policies/{policy_id}")

    def validate_policy(self, policy: Policy) -> Dict[str, Any]:
        return self._post("/rlaas/v1/policies/validate", policy.to_dict())

    def update_rollout(self, policy_id: str, rollout_percent: int) -> Dict[str, Any]:
        return self._post(f"/rlaas/v1/policies/{policy_id}/rollout", {"rollout_percent": rollout_percent})

    def rollback_policy(self, policy_id: str, version: int) -> Dict[str, Any]:
        return self._post(f"/rlaas/v1/policies/{policy_id}/rollback", {"version": version})

    def list_policy_audit(self, policy_id: str) -> List[Dict[str, Any]]:
        return self._get(f"/rlaas/v1/policies/{policy_id}/audit")

    def list_policy_versions(self, policy_id: str) -> List[Dict[str, Any]]:
        return self._get(f"/rlaas/v1/policies/{policy_id}/versions")

    def analytics_summary(self, top: Optional[int] = None) -> Dict[str, Any]:
        query = ""
        if top is not None:
            query = f"?top={top}"
        return self._get(f"/rlaas/v1/analytics/summary{query}")

    def _get(self, path: str) -> Any:
        response = self.session.get(self.base_url + path, timeout=self.timeout_seconds)
        return self._decode_response(response)

    def _post(self, path: str, body: Dict[str, Any]) -> Any:
        response = self.session.post(self.base_url + path, json=body, timeout=self.timeout_seconds)
        return self._decode_response(response)

    def _put(self, path: str, body: Dict[str, Any]) -> Any:
        response = self.session.put(self.base_url + path, json=body, timeout=self.timeout_seconds)
        return self._decode_response(response)

    def _delete(self, path: str) -> None:
        response = self.session.delete(self.base_url + path, timeout=self.timeout_seconds)
        if response.status_code >= 400:
            self._raise_http_error(response)

    @staticmethod
    def _decode_response(response: requests.Response) -> Any:
        if response.status_code >= 400:
            RlaasClient._raise_http_error(response)
        if not response.content:
            return None
        return response.json()

    @staticmethod
    def _raise_http_error(response: requests.Response) -> None:
        message = f"RLAAS API error ({response.status_code})"
        try:
            payload = response.json()
            if isinstance(payload, dict) and "error" in payload:
                message = f"{message}: {payload['error']}"
        except Exception:
            pass
        raise RuntimeError(message)
