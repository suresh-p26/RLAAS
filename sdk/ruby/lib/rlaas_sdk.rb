require_relative "rlaas_sdk/types"
require_relative "rlaas_sdk/client"

module Rlaas
  class Error < StandardError
    attr_reader :status_code

    def initialize(status_code, message)
      @status_code = status_code
      super("RLAAS API error (#{status_code}): #{message}")
    end
  end
end
