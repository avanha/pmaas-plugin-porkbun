// The button on the porkbun page: check every record again, and send the value last asked for again to any that has another. What
// it did is said beside it, and the page is loaded again once the plugin has had time to hear back.
class PorkbunStatus {
    constructor(doc) {
        this.doc = doc;
    }

    init = () => {
        const button = this.doc.querySelector("button.force-update");

        if (!button) {
            return false;
        }

        button.addEventListener("click", () => this.force(button));

        return true;
    }

    force = async (button) => {
        const result = button.parentElement.querySelector(".force-result");
        result.textContent = "";
        button.disabled = true;

        try {
            const response = await fetch("/plugins/porkbun/force", {
                method: "POST",
                headers: {"Content-Type": "application/json"},
                body: "{}",
            });

            if (!response.ok) {
                const reason = (await response.text()).replace(/^Failed to handle request: /, "").split("\n")[0].slice(0, 200);
                result.textContent = "Could not ask (" + response.status + "): " + reason;
                button.disabled = false;
                return;
            }

            const done = await response.json();
            const failed = (done.Failed || []).length;
            result.textContent = done.Updated + " sent again, " + done.Checked + " checked" + (failed ? ", " + failed + " failed: " + done.Failed.join("; ") : "");

            // The answers come back a moment later.
            setTimeout(() => location.reload(), failed ? 6000 : 4000);
        } catch (e) {
            result.textContent = "Could not ask: " + e;
            button.disabled = false;
        }
    }
}

new PorkbunStatus(document).init();
