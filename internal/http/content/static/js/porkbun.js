/*
 * The button on the porkbun page: check every record again, and send the value last asked for again to any that has another. What it did is
 * said beside it, and the page is loaded again once the plugin has had time to hear back.
 *
 * It runs on old browsers, an iPad that no longer gets updates among them (Safari 9): plain ES5, with no arrow functions, classes, class
 * fields, let or const, async functions or fetch, none of which it has, and a script that uses one of them does not run at all, which is a
 * button that cannot be clicked. Nothing here is slower or worse in a modern browser.
 *
 * The parts are given what they use from outside (the document, how a request is made, how the page is reloaded), so that they are tested
 * with fakes: see js_test/porkbun_test.js.
 */
(function (global) {
    "use strict";

    // Posts the body as JSON, and calls done(error, status, text), error being set if there was no answer at all.
    function postJson(url, body, done) {
        var request = new global.XMLHttpRequest();

        request.open("POST", url, true);
        request.setRequestHeader("Content-Type", "application/json");

        request.onload = function () {
            done(null, request.status, request.responseText);
        };

        request.onerror = function () {
            done(new Error("the request could not be sent"));
        };

        request.send(JSON.stringify(body));
    }

    // What the server says about a request it refused is about the request, so it is worth showing: its first line, without its prefix.
    function reasonFrom(text) {
        return String(text).replace(/^Failed to handle request: /, "").split("\n")[0].slice(0, 200);
    }

    // What forcing did, in a line: how many were sent again, how many were only checked, and what failed.
    function summary(done) {
        var failed = done.Failed || [];

        return done.Updated + " sent again, " + done.Checked + " checked" + (failed.length ? ", " + failed.length + " failed: " + failed.join("; ") : "");
    }

    function PorkbunStatus(doc, options) {
        options = options || {};
        this.doc = doc;
        this.post = options.post || postJson;
        this.later = options.later || global.setTimeout.bind(global);
        this.reload = options.reload || function () {
            global.location.reload();
        };
    }

    PorkbunStatus.prototype.init = function () {
        var self = this;
        var button = this.doc.querySelector("button.force-update");

        if (!button) {
            return false;
        }

        button.addEventListener("click", function () {
            self.force(button);
        });

        return true;
    };

    PorkbunStatus.prototype.force = function (button) {
        var self = this;
        var result = button.parentNode.querySelector(".force-result");
        result.textContent = "";
        button.disabled = true;

        this.post("/plugins/porkbun/force", {}, function (error, status, text) {
            if (error) {
                result.textContent = "Could not ask: " + error;
                button.disabled = false;
                return;
            }

            if (status < 200 || status > 299) {
                result.textContent = "Could not ask (" + status + "): " + reasonFrom(text);
                button.disabled = false;
                return;
            }

            var done;

            try {
                done = JSON.parse(text);
            } catch (e) {
                result.textContent = "Could not ask: the answer was not understood";
                button.disabled = false;
                return;
            }

            result.textContent = summary(done);

            // The answers come back a moment later.
            self.later(self.reload, (done.Failed || []).length ? 6000 : 4000);
        });
    };

    var exported = {PorkbunStatus: PorkbunStatus, reasonFrom: reasonFrom, summary: summary};

    if (typeof module !== "undefined" && module.exports) {
        module.exports = exported;
    }

    if (typeof document !== "undefined" && global.addEventListener) {
        new PorkbunStatus(document).init();
    }
})(typeof window !== "undefined" ? window : globalThis);
