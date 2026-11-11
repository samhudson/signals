class Signals < Formula
  desc "Menu bar lights for your AI agent sessions, and what they use"
  homepage "https://github.com/samhudson/signals"
  url "https://github.com/samhudson/signals/releases/download/v0.0.0/signals-0.0.0.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"
  license "MIT"

  depends_on "go" => :build
  depends_on "jq"
  depends_on :macos

  def install
    # A bottle is built for Apple silicon and Intel at once.
    ENV["SIGNALS_UNIVERSAL"] = "1" if build.bottle?
    # The menu bar app (Swift) and the engine that counts each agent's API calls (Go).
    system "./script/build-app", buildpath/"libexec"
    system "./script/build-core", buildpath/"libexec/engine"
    libexec.install Dir["libexec/*"]
    bin.install "bin/signals"
  end

  def caveats
    <<~EOS
      Set up your agents (Claude Code, Codex, Gemini CLI, Cursor, Copilot):
        signals install

      Signals lives in your menu bar and starts at login.
    EOS
  end

  test do
    assert_path_exists libexec/"Signals.app/Contents/MacOS/Signals"
    assert_match version.to_s, shell_output("#{bin}/signals --version")
    assert_path_exists libexec/"signals-hook"
    assert_path_exists libexec/"signals-meter"
    assert_match "\"tools\"", shell_output("#{libexec}/engine/signals-core usage summary --json --no-scan")
  end
end
