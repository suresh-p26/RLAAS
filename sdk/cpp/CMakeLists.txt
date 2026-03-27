cmake_minimum_required(VERSION 3.16)
project(rlaas_sdk VERSION 0.1.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

# ── Dependencies ──────────────────────────────────────────────────────────────
find_package(CURL REQUIRED)

include(FetchContent)
FetchContent_Declare(
  nlohmann_json
  GIT_REPOSITORY https://github.com/nlohmann/json.git
  GIT_TAG        v3.11.3
)
FetchContent_MakeAvailable(nlohmann_json)

# ── Library ───────────────────────────────────────────────────────────────────
add_library(rlaas_sdk STATIC src/client.cpp)
add_library(rlaas::sdk ALIAS rlaas_sdk)

target_include_directories(rlaas_sdk
  PUBLIC
    $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
    $<INSTALL_INTERFACE:include>
)

target_link_libraries(rlaas_sdk
  PUBLIC
    CURL::libcurl
    nlohmann_json::nlohmann_json
)

# ── Install ───────────────────────────────────────────────────────────────────
install(TARGETS rlaas_sdk EXPORT rlaas_sdkTargets ARCHIVE DESTINATION lib)
install(DIRECTORY include/ DESTINATION include)
