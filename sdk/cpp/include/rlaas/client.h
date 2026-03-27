#pragma once
#include <map>
#include <optional>
#include <stdexcept>
#include <string>
#include <vector>

namespace rlaas {

// ── Exception ─────────────────────────────────────────────────────────────────

struct RlaasException : public std::runtime_error {
    int status_code;
    RlaasException(int code, const std::string& msg)
        : std::runtime_error(msg), status_code(code) {}
};

// ── Request / Response types ──────────────────────────────────────────────────

struct CheckRequest {
    std::string request_id;
    std::string org_id;
    std::string tenant_id;
    std::string signal_type;
    std::string operation;
    std::string endpoint;
    std::string method;
    // optional fields
    std::string user_id;
    std::string api_key;
    std::string client_id;
    std::string source_ip;
    std::string region;
    std::string environment;
    std::map<std::string, std::string> tags;
};

struct Decision {
    bool        allowed        = false;
    std::string action;
    std::string reason;
    int         remaining      = 0;
    long long   retry_after_ms = 0;
    std::string policy_id;
};

// ── Client ────────────────────────────────────────────────────────────────────

class Client {
public:
    // Construct with base URL (e.g. "http://localhost:8080").
    // timeout_ms controls connect + transfer timeout.
    explicit Client(std::string base_url, long timeout_ms = 5000);
    ~Client();

    // Not copyable — libcurl handles are move-only.
    Client(const Client&)            = delete;
    Client& operator=(const Client&) = delete;
    Client(Client&&)                 = default;
    Client& operator=(Client&&)      = default;

    // ── Decision API ──────────────────────────────────────────────────────────
    Decision    check(const CheckRequest& req);
    std::string acquire(const std::string& json_body);
    std::string release(const std::string& lease_id);

    // ── Policy management ─────────────────────────────────────────────────────
    std::string list_policies();
    std::string get_policy(const std::string& policy_id);
    std::string create_policy(const std::string& json_body);
    std::string update_policy(const std::string& policy_id, const std::string& json_body);
    void        delete_policy(const std::string& policy_id);
    std::string validate_policy(const std::string& json_body);

    // ── Lifecycle ─────────────────────────────────────────────────────────────
    std::string update_rollout(const std::string& policy_id, int rollout_percent);
    std::string rollback_policy(const std::string& policy_id, long long version);

    // ── History ───────────────────────────────────────────────────────────────
    std::string list_policy_audit(const std::string& policy_id);
    std::string list_policy_versions(const std::string& policy_id);

    // ── Analytics ─────────────────────────────────────────────────────────────
    std::string analytics_summary(std::optional<int> top = std::nullopt);

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

} // namespace rlaas
