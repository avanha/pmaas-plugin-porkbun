"use strict";
// Tests for content/static/js/porkbun.js, with a document and a request that are fakes. Run with: node js_test/porkbun_test.js
// (the Go test in oldbrowsers_test.go does).
var assert = require("assert");
var fs = require("fs");
var script = require("../content/static/js/porkbun.js");

var tests = [];
function test(name, fn) { tests.push([name, fn]); }

function fakePage() {
    var result = {textContent: ""};
    var button = {disabled: false, listeners: {}, addEventListener: function (type, fn) { this.listeners[type] = fn; },
        parentNode: {querySelector: function () { return result; }}};

    return {button: button, result: result, doc: {querySelector: function () { return button; }}};
}

function porkbun(page) {
    var posts = [];
    var laters = [];
    var p = new script.PorkbunStatus(page.doc, {
        post: function (url, body, done) { posts.push({url: url, body: body, done: done}); },
        later: function (fn, ms) { laters.push({fn: fn, ms: ms}); },
        reload: function () {}
    });

    return {p: p, posts: posts, laters: laters};
}

test("the button posts to force and says what was done, and the page is loaded again after a while", function () {
    var page = fakePage();
    var t = porkbun(page);
    assert.strictEqual(t.p.init(), true);

    page.button.listeners.click();

    assert.strictEqual(t.posts[0].url, "/plugins/porkbun/force");
    assert.deepStrictEqual(t.posts[0].body, {});
    assert.strictEqual(page.button.disabled, true);

    t.posts[0].done(null, 200, JSON.stringify({Updated: 1, Checked: 3, Failed: null}));

    assert.strictEqual(page.result.textContent, "1 sent again, 3 checked");
    assert.strictEqual(t.laters[0].ms, 4000);
});

test("what failed is said, and the page waits a little longer", function () {
    var page = fakePage();
    var t = porkbun(page);
    t.p.init();

    page.button.listeners.click();
    t.posts[0].done(null, 200, JSON.stringify({Updated: 0, Checked: 1, Failed: ["a: nope", "b: no"]}));

    assert.strictEqual(page.result.textContent, "0 sent again, 1 checked, 2 failed: a: nope; b: no");
    assert.strictEqual(t.laters[0].ms, 6000);
});

test("a refusal, no answer and an answer that is not understood are said, and the button comes back", function () {
    var page = fakePage();
    var t = porkbun(page);
    t.p.init();

    page.button.listeners.click();
    t.posts[0].done(null, 403, "Failed to handle request: plugin is not running\nmore");
    assert.strictEqual(page.result.textContent, "Could not ask (403): plugin is not running");
    assert.strictEqual(page.button.disabled, false);

    page.button.listeners.click();
    t.posts[1].done(new Error("the request could not be sent"));
    assert.ok(page.result.textContent.indexOf("Could not ask: ") === 0);
    assert.strictEqual(page.button.disabled, false);

    page.button.listeners.click();
    t.posts[2].done(null, 200, "<html>");
    assert.strictEqual(page.result.textContent, "Could not ask: the answer was not understood");
    assert.strictEqual(page.button.disabled, false);
    assert.strictEqual(t.laters.length, 0);
});

test("init says whether there was a button on the page", function () {
    var p = new script.PorkbunStatus({querySelector: function () { return null; }}, {});
    assert.strictEqual(p.init(), false);
});

// The lint every script's test has: what Safari 9 does not have, looked for in the source. Pasted into each test.
test("the script uses nothing Safari 9 does not have", function () {
    var source = fs.readFileSync(__dirname + "/../content/static/js/porkbun.js", "utf8").replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/.*$/gm, "");

    [
        [/=>/, "an arrow function"], [/\bclass\s+\w/, "a class"], [/\blet\s/, "let"], [/\bconst\s/, "const"], [/`/, "a template literal"],
        [/\basync\b/, "async"], [/\bawait\b/, "await"], [/\bfetch\s*\(/, "fetch"], [/\.\.\./, "spread"], [/\?\./, "optional chaining"],
        [/\?\?/, "nullish coalescing"], [/\bObject\.(entries|values)\b/, "Object.entries or values"], [/\.padStart\b|\.padEnd\b/, "padStart"]
    ].forEach(function (pair) {
        assert.ok(!pair[0].test(source), "the script has " + pair[1]);
    });

    // forEach is only for arrays: a NodeList has none before Safari 10, which is what toArray is for.
    (source.match(/(\w+)\.forEach/g) || []).forEach(function (use) {
        assert.ok([].indexOf(use) >= 0, "forEach on " + use);
    });
});

var failed = 0;
tests.forEach(function (t) {
    try { t[1](); console.log("ok   " + t[0]); } catch (e) { failed++; console.log("FAIL " + t[0] + "\n     " + (e && e.stack || e)); }
});
if (failed) { process.exit(1); }
