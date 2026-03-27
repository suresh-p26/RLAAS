module Rlaas
  # Immutable value object returned by {Client#check}.
  Decision = Struct.new(:allowed, :action, :reason, :remaining, :retry_after, :policy_id,
                        keyword_init: true) do
    def allowed? = allowed
  end
end
