# Homebrew formula for flowlio (DESIGN-WAKE §4.1, §12 — the packaging decision).
#
# `brew install coddyum/flowlio/flowlio` installs ONE program the user drives with a single command:
#
#   flowlio up        self-host: a managed Postgres 18 container + the engine + the waker
#   flowlio up        hosted (FLOWLIO_MODE=hosted): the waker only, engine on our infra
#
# Two binaries are built, and that is not two programs: `flowlio` is the CLI, the MCP server and the
# waker in one; `flowlio-api` is the engine `flowlio up` starts in self-host. The waker is a MODE of
# `flowlio`, never a separate binary — the user installs and runs one thing.
#
# When this repository is published, replace `head`/`url` with the tagged release tarball and its
# sha256, and drop `head` if a rolling install is not wanted. Postgres is NOT a formula dependency:
# self-host runs it in a container flowlio manages (D38, loopback-bound), so Docker — not a brew
# Postgres — is what a self-host user needs, and the formula says so in its caveats rather than
# pulling a database nobody asked for.
class Flowlio < Formula
  desc "Project manager for AI agents: cross-repo issues, and a waker that closes the loop"
  homepage "https://github.com/Coddyum/flowlio-agents"
  license "UNLICENSED"
  head "https://github.com/Coddyum/flowlio-agents.git", branch: "main"

  depends_on "go" => :build

  def install
    ldflags = "-s -w"
    system "go", "build", *std_go_args(ldflags: ldflags, output: bin/"flowlio"), "./cmd/flowlio"
    system "go", "build", *std_go_args(ldflags: ldflags, output: bin/"flowlio-api"), "./cmd/api"
  end

  def caveats
    <<~EOS
      Self-host runs Postgres 18 in a container flowlio manages — install Docker first, then:

        flowlio up

      Hosted (the engine runs on our infra) needs only the waker:

        FLOWLIO_MODE=hosted flowlio up

      One repository at a time is made operational from its root with:

        flowlio connect <REPO>
    EOS
  end

  test do
    assert_match "flowlio", shell_output("#{bin}/flowlio version")
  end
end
