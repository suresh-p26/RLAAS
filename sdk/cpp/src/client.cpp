#include "rlaas/client.h"

#include <curl/curl.h>
#include <nlohmann/json.hpp>
#include <sstream>

using json = nlohmann::json;

namespace rlaas {

// ── libcurl write callback ────────────────────────────────────────────────────

static size_t write_cb(char* ptr, size_t size, size_t nmemb, void* userdata) {
    auto* buf = static_cast<std::string*>(userdata);
    buf->append(ptr, size * nmemb);
    return size * nmemb;
}

// ── Pimpl ─────────────────────────────────────────────────────────────────────

struct Client::Impl {
    std::string base_url;
    long        timeout_ms;
    CURL*       curl;

    Impl(std::string url, long ms)
        : base_url(std::move(url)), timeout_ms(ms), curl(curl_easy_init()) {
        if (!curl) throw RlaasException(0, "curl_easy_init failed");
        // Strip trailing slash
        while (!base_url.empty() && base_url.back() == '/') base_url.pop_back();
    }

    ~Impl() {
        if (curl) curl_easy_cleanup(curl);
    }

    // Returns response body; throws RlaasException on HTTP 4xx/5xx.
    std::string request(const std::string& method, const std::string& path,
                        const std::string& body = "") {
        std::string url = base_url + path;
        std::string response_body;

        curl_easy_reset(curl);
        curl_easy_setopt(curl, CURLOPT_URL, url.c_str());
        curl_easy_setopt(curl, CURLOPT_TIMEOUT_MS, timeout_ms);
        curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, write_cb);
        curl_easy_setopt(curl, CURLOPT_WRITEDATA, &response_body);

        struct curl_slist* headers = nullptr;
        headers = curl_slist_append(headers, "Content-Type: application/json");
        headers = curl_slist_append(headers, "Accept: application/json");
        curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);

        if (method == "POST") {
            curl_easy_setopt(curl, CURLOPT_POST, 1L);
            curl_easy_setopt(curl, CURLOPT_POSTFIELDS, body.c_str());
            curl_easy_setopt(curl, CURLOPT_POSTFIELDSIZE, static_cast<long>(body.size()));
        } else if (method == "PUT") {
            curl_easy_setopt(curl, CURLOPT_CUSTOMREQUEST, "PUT");
            curl_easy_setopt(curl, CURLOPT_POSTFIELDS, body.c_str());
            curl_easy_setopt(curl, CURLOPT_POSTFIELDSIZE, static_cast<long>(body.size()));
        } else if (method == "DELETE") {
            curl_easy_setopt(curl, CURLOPT_CUSTOMREQUEST, "DELETE");
        }
        // GET is the default

        CURLcode res = curl_easy_perform(curl);
        curl_slist_free_all(headers);

        if (res != CURLE_OK) {
            throw RlaasException(0, curl_easy_strerror(res));
        }

        long http_code = 0;
        curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, &http_code);
        if (http_code >= 400) {
            std::string msg = "HTTP " + std::to_string(http_code);
            try {
                auto j = json::parse(response_body);
                if (j.contains("error")) msg += ": " + j["error"].get<std::string>();
            } catch (...) {}
            throw RlaasException(static_cast<int>(http_code), msg);
        }

        return response_body;
    }
};

// ── Client ────────────────────────────────────────────────────────────────────

Client::Client(std::string base_url, long timeout_ms)
    : impl_(std::make_unique<Impl>(std::move(base_url), timeout_ms)) {}

Client::~Client() = default;

// ── Decision ──────────────────────────────────────────────────────────────────

Decision Client::check(const CheckRequest& req) {
    json j;
    j["request_id"]  = req.request_id;
    j["org_id"]      = req.org_id;
    j["tenant_id"]   = req.tenant_id;
    j["signal_type"] = req.signal_type;
    j["operation"]   = req.operation;
    j["endpoint"]    = req.endpoint;
    j["method"]      = req.method;
    if (!req.user_id.empty())    j["user_id"]     = req.user_id;
    if (!req.api_key.empty())    j["api_key"]     = req.api_key;
    if (!req.client_id.empty())  j["client_id"]   = req.client_id;
    if (!req.source_ip.empty())  j["source_ip"]   = req.source_ip;
    if (!req.region.empty())     j["region"]      = req.region;
    if (!req.environment.empty())j["environment"] = req.environment;
    if (!req.tags.empty())       j["tags"]        = req.tags;

    auto body = impl_->request("POST", "/rlaas/v1/check", j.dump());
    auto resp = json::parse(body);

    Decision d;
    d.allowed        = resp.value("allowed", false);
    d.action         = resp.value("action", "");
    d.reason         = resp.value("reason", "");
    d.remaining      = resp.value("remaining", 0);
    d.retry_after_ms = resp.value("retry_after_ms", 0LL);
    d.policy_id      = resp.value("policy_id", "");
    return d;
}

std::string Client::acquire(const std::string& json_body) {
    return impl_->request("POST", "/rlaas/v1/acquire", json_body);
}

std::string Client::release(const std::string& lease_id) {
    json j; j["lease_id"] = lease_id;
    return impl_->request("POST", "/rlaas/v1/release", j.dump());
}

// ── Policy management ─────────────────────────────────────────────────────────

std::string Client::list_policies() {
    return impl_->request("GET", "/rlaas/v1/policies");
}

std::string Client::get_policy(const std::string& policy_id) {
    return impl_->request("GET", "/rlaas/v1/policies/" + policy_id);
}

std::string Client::create_policy(const std::string& json_body) {
    return impl_->request("POST", "/rlaas/v1/policies", json_body);
}

std::string Client::update_policy(const std::string& policy_id, const std::string& json_body) {
    return impl_->request("PUT", "/rlaas/v1/policies/" + policy_id, json_body);
}

void Client::delete_policy(const std::string& policy_id) {
    impl_->request("DELETE", "/rlaas/v1/policies/" + policy_id);
}

std::string Client::validate_policy(const std::string& json_body) {
    return impl_->request("POST", "/rlaas/v1/policies/validate", json_body);
}

// ── Lifecycle ─────────────────────────────────────────────────────────────────

std::string Client::update_rollout(const std::string& policy_id, int rollout_percent) {
    json j; j["rollout_percent"] = rollout_percent;
    return impl_->request("POST", "/rlaas/v1/policies/" + policy_id + "/rollout", j.dump());
}

std::string Client::rollback_policy(const std::string& policy_id, long long version) {
    json j; j["version"] = version;
    return impl_->request("POST", "/rlaas/v1/policies/" + policy_id + "/rollback", j.dump());
}

// ── History ───────────────────────────────────────────────────────────────────

std::string Client::list_policy_audit(const std::string& policy_id) {
    return impl_->request("GET", "/rlaas/v1/policies/" + policy_id + "/audit");
}

std::string Client::list_policy_versions(const std::string& policy_id) {
    return impl_->request("GET", "/rlaas/v1/policies/" + policy_id + "/versions");
}

// ── Analytics ─────────────────────────────────────────────────────────────────

std::string Client::analytics_summary(std::optional<int> top) {
    std::string path = "/rlaas/v1/analytics/summary";
    if (top) path += "?top=" + std::to_string(*top);
    return impl_->request("GET", path);
}

} // namespace rlaas
