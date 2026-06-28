#!/bin/bash
# Build ustreamer v6.60 from source. The apt 5.4 aborts on v4l2loopback with
# "Got unexpected writing event" (it watches the fd for write-readiness); 6.x
# dropped that check, so it streams the loopback fine.
set -e
echo "== install build deps =="
sudo apt-get install -y build-essential libevent-dev libbsd-dev libjpeg-dev pkg-config git
echo "== fetch + build ustreamer v6.60 =="
cd "$HOME"
rm -rf ustreamer-src
git clone --depth=1 --branch v6.60 https://github.com/pikvm/ustreamer.git ustreamer-src
cd ustreamer-src
make -j"$(nproc)"
echo "== install to /usr/local/bin =="
sudo make install
hash -r
echo "== verify (expect 6.60, path /usr/local/bin/ustreamer) =="
command -v ustreamer
ustreamer --version
