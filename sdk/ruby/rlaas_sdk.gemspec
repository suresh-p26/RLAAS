Gem::Specification.new do |spec|
  spec.name          = "rlaas-sdk"
  spec.version       = "0.1.0"
  spec.authors       = ["RLAAS"]
  spec.summary       = "Ruby SDK for RLAAS HTTP APIs"
  spec.description   = "Policy-driven rate limiting client for RLAAS — zero runtime dependencies."
  spec.license       = "Apache-2.0"
  spec.homepage      = "https://github.com/rlaas-io/rlaas"

  spec.required_ruby_version = ">= 3.0"

  spec.files         = Dir["lib/**/*.rb", "README.md", "rlaas_sdk.gemspec"]
  spec.require_paths = ["lib"]
end
