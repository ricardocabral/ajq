---
title: "Privacy policy"
description: "How ajq, its coding-agent skill, and its documentation handle data."
---

{{% blocks/section color="white" %}}

# Privacy policy

Last updated: October 9, 2026.

This policy covers the open-source ajq command-line tool, the ajq coding-agent
skill, and this documentation website.

## Data collection

ajq does not send usage telemetry or input data to its maintainer. The coding-agent
skill has no hosted service and does not collect information for the maintainer.
Installing the skill does not install the ajq executable, provision a model, or
authorize a cloud backend.

The CLI processes the JSON or NDJSON that you provide. Those records may contain
personal information if you include it. ajq uses the records to run your query
and produce its output; it does not sell that information or use it for advertising.

## Local and cloud processing

Ordinary jq queries do not call a model backend. The mock backend also works
without network access or a model.

Semantic operations send the values being judged and their operation instructions
to the backend you select. The managed local backend runs on your computer.
Ollama uses the server you configure, which may be local or remote. If you select
a cloud backend, such as OpenAI, OpenRouter, or Anthropic, the selected provider
receives the semantic request. Its own privacy policy, retention rules, and your
account settings govern that processing. Selecting a custom endpoint sends the
request to that endpoint's operator.

Model or engine provisioning and software installation may contact the download
hosts used by those commands. Those hosts receive normal download-request
information, such as your IP address; they do not receive your query input as
part of a download.

## Local storage and retention

By default, ajq stores successful semantic judgments in a cache on your computer.
Cache entries include the judged values, operation instructions, model identity,
results, and creation time. They can contain personal information from your input.
The cache has no automatic expiration and remains until you clear it or remove
its files.

Use `--no-cache` to bypass cache reads and writes for a run. Use `ajq cache status`
to locate the cache and `ajq cache clear` to remove cached judgments. Clearing the
cache does not remove your input files, saved output, installed engines, or models.
See [Manage the cache](../docs/how-to/manage-the-cache/) for these controls.

Input files and any output you save remain under your control. ajq does not delete
them after processing. Your shell, coding-agent environment, configured server,
or chosen model provider may keep their own records under their own policies.

## Documentation and support

The documentation website does not configure visitor analytics or advertising
tracking. GitHub Pages hosts it; GitHub may process normal web-request information
under [GitHub's privacy statement](https://docs.github.com/en/site-policy/privacy-policies/github-general-privacy-statement).
The site also loads fonts from Google Fonts. Your browser sends normal request
information, including your IP address, to Google when fetching those fonts;
see [Google Fonts' FAQ](https://fonts.google.com/faq).

If you open a public GitHub issue or discussion, the information you submit is
visible to the maintainer and others. Do not include private input data, credentials,
or sensitive cache files. GitHub's own policies govern the storage of those posts;
ajq does not set a separate retention period for them.

## Contact and changes

For questions about ajq's data handling, use the
[project issue tracker](https://github.com/ricardocabral/ajq/issues).
Changes to this policy will be published on this page with an updated date.

{{% /blocks/section %}}
