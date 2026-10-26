// No standalone preset or its topbar: that topbar's logo SVG carries an
// embedded <style>, which the server-level CSP (style-src 'self') blocks and
// logs as a violation. The base layout renders the same API console without
// that logo, so the page satisfies the CSP unchanged.
window.onload = function () {
  window.ui = SwaggerUIBundle({
    url: "/openapi.json",
    dom_id: "#swagger-ui",
    deepLinking: true,
    presets: [SwaggerUIBundle.presets.apis],
    layout: "BaseLayout"
  });
};
