// get.windlass.run — the install script's vanity URL.
//
//   curl -fsSL https://get.windlass.run | sudo sh
//
// A 302 rather than a 301: the target moves with every release, and a
// permanently cached redirect would pin whoever ran it first to whichever
// release happened to be current that day.
//
// install.sh is published as a release asset, so the script someone pipes to
// root is the one that shipped with those binaries rather than whatever is on
// main at the time.
const REPO = "Sulaiman-Dauda/windlass";
const LATEST = `https://github.com/${REPO}/releases/latest/download/install.sh`;

export default {
  async fetch(request) {
    const url = new URL(request.url);

    // Pin a version: get.windlass.run/v0.31
    const tag = url.pathname.replace(/^\/+|\/+$/g, "");
    const target = /^v\d+\.\d+(\.\d+)?$/.test(tag)
      ? `https://github.com/${REPO}/releases/download/${tag}/install.sh`
      : LATEST;

    return new Response(null, {
      status: 302,
      headers: {
        Location: target,
        "Cache-Control": "no-store",
        "Referrer-Policy": "strict-origin-when-cross-origin",
        "X-Content-Type-Options": "nosniff",
      },
    });
  },
};
