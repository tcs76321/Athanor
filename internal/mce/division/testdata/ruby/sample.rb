require "json"

class Counter
  def initialize
    @counts = Hash.new(0)
  end

  def add(key)
    @counts[key] += 1
  end

  def get(key)
    @counts[key]
  end
end

c = Counter.new
c.add("a")
puts c.get("a")
