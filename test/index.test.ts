// You can import your modules
// import index from '../src/index'

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import nock from "nock";
import { Probot, ProbotOctokit } from "probot";
import { afterEach, beforeEach, describe, expect, test } from "vitest";

import myProbotApp from "../src/index";

const issueCreatedBody = {
  body:
    `Beep, boop 🤖  Hi, I'm instruct-lab-bot and I'm going to help you` +
    `with your pull request. Thanks for you contribution! 🎉\n` +
    `In order to proceed please reply with the following comment:\n` +
    `\`@instruct-lab-bot generate\`\n` +
    `This will trigger the generation of some test data for your` +
    `contribution. Once the data is generated, I will let you know` +
    `and you can proceed with the review.`,
};

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const privateKey = fs.readFileSync(
  path.join(__dirname, "fixtures/mock-cert.pem"),
  "utf-8",
);

const payload = JSON.parse(
  fs.readFileSync(path.join(__dirname, "fixtures/pr.opened.json"), "utf-8"),
);

describe("My Probot app", () => {
  let probot: Probot;

  beforeEach(() => {
    nock.disableNetConnect();
    probot = new Probot({
      appId: 123,
      privateKey,
      // disable request throttling and retries for testing
      Octokit: ProbotOctokit.defaults({
        retry: { enabled: false },
        throttle: { enabled: false },
      }),
    });
    // Load our app into probot
    probot.load(myProbotApp);
  });

  test("creates a comment when an PR is opened", async () => {
    const mock = nock("https://api.github.com")
      // Test that we correctly return a test token
      .post("/app/installations/2/access_tokens")
      .reply(200, {
        token: "test",
        permissions: {
          issues: "write",
          pull_request: "write",
        },
      })

      // Test that a comment is posted
      .post(
        "/repos/instruct-lab-bot/taxonomy/issues/1/comments",
        (body: unknown) => {
          expect(body).toMatchObject(issueCreatedBody);
          return true;
        },
      )
      .reply(200);

    // Receive a webhook event
    await probot.receive({ id: "1234", name: "pull_request", payload });

    expect(mock.pendingMocks()).toStrictEqual([]);
  });

  afterEach(() => {
    nock.cleanAll();
    nock.enableNetConnect();
  });
});

// For more information about testing with Jest see:
// https://facebook.github.io/jest/

// For more information about using TypeScript in your tests, Jest recommends:
// https://github.com/kulshekhar/ts-jest

// For more information about testing with Nock see:
// https://github.com/nock/nock
