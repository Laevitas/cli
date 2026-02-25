# Homebrew formula for Laevitas CLI
# Repository: https://github.com/laevitas/homebrew-cli
#
# To install:
#   brew tap laevitas/cli
#   brew install laevitas

class Laevitas < Formula
  desc "Crypto derivatives analytics from your terminal"
  homepage "https://cli.laevitas.ch"
  version "0.1.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/laevitas/cli/releases/download/v#{version}/laevitas-darwin-arm64"
      sha256 "PLACEHOLDER_SHA256"
    end
    on_intel do
      url "https://github.com/laevitas/cli/releases/download/v#{version}/laevitas-darwin-amd64"
      sha256 "PLACEHOLDER_SHA256"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/laevitas/cli/releases/download/v#{version}/laevitas-linux-arm64"
      sha256 "PLACEHOLDER_SHA256"
    end
    on_intel do
      url "https://github.com/laevitas/cli/releases/download/v#{version}/laevitas-linux-amd64"
      sha256 "PLACEHOLDER_SHA256"
    end
  end

  def install
    binary_name = "laevitas"
    # The downloaded file is already the binary
    bin.install Dir["laevitas-*"].first => binary_name
  end

  test do
    assert_match "laevitas", shell_output("#{bin}/laevitas --version")
  end
end
