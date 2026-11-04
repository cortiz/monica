# Install

Pick one; each gives you a `monica` command. Check it with `monica --version`.

* **Binary** (Linux, macOS, Windows, FreeBSD) — download the archive for your system from the
  [releases page](https://github.com/cortiz/monica/releases), unpack it and put `monica` on
  your `PATH`:
  ```bash
  # Linux x86_64 shown; the others are monica_<Os>_<arch>.tar.gz (Darwin, Linux, Freebsd;
  # x86_64, arm64, armv6, riscv64) and monica_Windows_<arch>.zip
  VERSION=v1.0.0-rc.2
  curl -LO https://github.com/cortiz/monica/releases/download/$VERSION/monica_Linux_x86_64.tar.gz
  tar -xzf monica_Linux_x86_64.tar.gz monica
  sudo mv monica /usr/local/bin/
  ```
  Each release has a `checksums.txt` (`sha256sum -c checksums.txt --ignore-missing`).
  Archives also include the Readme and the [examples](https://github.com/cortiz/monica/blob/main/examples/README.md). On macOS, a
  binary downloaded with a browser is quarantined; clear it with `xattr -d com.apple.quarantine monica`.
* **Homebrew** (macOS, from v1.0.0):
  ```bash
  brew install cortiz/tap/monica
  ```
  Upgrade with `brew upgrade monica`.
* **Docker** (linux/amd64 and linux/arm64) — the image's entrypoint is `monica` and its
  working directory `/work`, so mount your project there:
  ```bash
  docker run --rm -v "$PWD:/work" ghcr.io/cortiz/monica:1.0.0-rc.2 run -r 3
  ```
  Tags: `1.2.3` and `1.2` for each release, `latest` for the newest stable one; release
  candidates (`1.0.0-rc.2`) only get their own tag.
* **From source** — with Go 1.27+, clone the repository and run `make build`; the binary is
  `./monica`.

Requests without a `User-Agent` header send `monica/<version>`.
