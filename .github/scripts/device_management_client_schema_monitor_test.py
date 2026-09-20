import copy
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import device_management_client_schema_monitor as m
from device_management_client_schema_diagnostics import findings, diagnosis


PIN, NEXT, PROJECT = 'a' * 40, 'b' * 40, 'c' * 40


def report(kind='seed', commit=NEXT, ref='seed_future'):
    return {'schemaVersion': 4, 'projectCommit': PROJECT, 'branch': m.entry(kind, ref, commit),
            'controlCommit': PIN, 'complete': True, 'automationErrors': [], 'findings': [],
            'stages': {s: {'state': 'passed'} for s in m.STAGES}}


def control():
    return report('control', PIN, 'control')


def manifest(*results):
    return {'schemaVersion': 4, 'complete': True, 'projectCommit': PROJECT, 'control': control()['branch'],
            'branches': [r['branch'] for r in results], 'upstream': 'upstream', 'canaryMirror': 'mirror'}


def failure(result):
    result['stages']['generate']['state'] = 'failed'
    item = diagnosis('parse', 'cannot unmarshal !!map into []schemagen.Example')
    item.update(location='internal/schemagen/model.go#L406', examples=[],
                evidence=[{'path': 'mdm/commands/a.yaml', 'detail': 'cannot unmarshal !!map into []schemagen.Example'}])
    result['findings'] = [item]
    return result


def actions(existing, *results):
    return m.issue_actions(existing, [control(), *results], manifest(*results), 'https://example.com/run', 'owner/repo')


def stored(action):
    return dict(action[2], number=42, state='open')


class DiscoveryTests(unittest.TestCase):
    def test_real_git_discovery_ignores_archived_and_promoted_seeds(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            apple, project = root / 'apple', root / 'project'
            for repo in (apple, project):
                repo.mkdir()
                m.run(['git', 'init', '-q', '-b', 'release'], repo)
                m.run(['git', 'config', 'user.name', 'Test'], repo)
                m.run(['git', 'config', 'user.email', 'test@example.com'], repo)
            (apple / 'data').write_text('old')
            m.run(['git', 'add', '.'], apple)
            m.run(['git', 'commit', '-qm', 'old release'], apple)
            old = m.run(['git', 'rev-parse', 'HEAD'], apple).strip()
            m.run(['git', 'branch', 'seed_OS_27_0'], apple)
            (apple / 'data').write_text('current')
            m.run(['git', 'commit', '-qam', 'current'], apple)
            pin = m.run(['git', 'rev-parse', 'HEAD'], apple).strip()
            m.run(['git', 'branch', 'schema-source/seed-old/' + old], apple)
            m.run(['git', 'switch', '-qc', 'seed_macOS_28'], apple)
            (apple / 'data').write_text('new schema')
            m.run(['git', 'commit', '-qam', 'future'], apple)
            future = m.run(['git', 'rev-parse', 'HEAD'], apple).strip()
            m.run(['git', 'switch', '-q', 'release'], apple)
            (project / m.SCHEMA).mkdir(parents=True)
            m.write_json(project / m.SCHEMA / 'GENERATED_FROM.json', {'commit': pin})
            m.run(['git', 'add', '.'], project)
            m.run(['git', 'update-index', '--add', '--cacheinfo', '160000,' + pin + ',' + m.CURRENT], project)
            m.run(['git', 'commit', '-qm', 'project'], project)
            result = m.discover(project, root / 'discovery.json', str(apple), str(apple))
            self.assertTrue(result['complete'], result)
            self.assertEqual(pin, result['control']['commit'])
            self.assertEqual([future], [b['commit'] for b in result['branches']])
            self.assertEqual('seed_macOS_28', result['branches'][0]['ref'])
            # Promotion is assessed as a new release, not as a stale seed.
            m.run(['git', 'merge', '--ff-only', 'seed_macOS_28'], apple)
            result = m.discover(project, root / 'discovery.json', str(apple), str(apple))
            self.assertEqual(['release'], [b['kind'] for b in result['branches']])

    def test_default_discovery_is_not_version_named(self):
        for default in ('release', 'main'):
            value = f'ref: refs/heads/{default}\tHEAD\n{PIN}\trefs/heads/{default}\n{NEXT}\trefs/heads/seed_999\n'
            self.assertEqual(default, m.parse_refs(value)[0])
        for value in ('', f'ref: refs/heads/seed_only\tHEAD\n{PIN}\trefs/heads/seed_only\n'):
            with self.assertRaises(ValueError):
                m.parse_refs(value)

    def test_discovery_failure_is_retained(self):
        with tempfile.TemporaryDirectory() as temp, patch.object(m, 'run', side_effect=OSError('offline')):
            result = m.discover(Path(temp), Path(temp) / 'discovery.json')
            self.assertFalse(result['complete'])
            self.assertEqual('offline', result['error'])

    def test_snapshot_identity_is_safe(self):
        for value in ('seed/../x', '.seed', 'seed/'):
            with self.assertRaises(ValueError):
                m.snapshot_ref(value, NEXT)
        self.assertEqual('refs/heads/schema-source/seed_future/' + NEXT, m.snapshot_ref('seed_future', NEXT))
        self.assertNotEqual(m.entry('seed', 'seed_os_future', NEXT)['key'],
                            m.entry('seed', 'seed_mac_future', NEXT)['key'])

    def test_capture_reuses_immutable_ref_and_rejects_mismatch(self):
        value = dict(manifest(report()), branches=[report()['branch']])
        with tempfile.TemporaryDirectory() as temp, patch.object(m, 'run', return_value=NEXT + '\tref'):
            self.assertTrue(m.capture_canaries(value, Path(temp) / 'capture.json')['complete'])
        with tempfile.TemporaryDirectory() as temp, patch.object(m, 'run', return_value=PIN + '\tref'):
            self.assertFalse(m.capture_canaries(value, Path(temp) / 'capture.json')['complete'])


class DiagnosticTests(unittest.TestCase):
    def test_hundreds_of_parse_errors_produce_one_cause_with_remediation(self):
        audit = {'findings': [{'stage': 'parse', 'kind': 'failure', 'evidence': [
            {'path': f'mdm/commands/{i}.yaml', 'detail': f'decode: yaml: unmarshal errors:\n  line {i}: cannot unmarshal !!map into []schemagen.Example'}]}
            for i in range(302)]}
        with tempfile.TemporaryDirectory() as temp:
            groups = findings('generate', 'error', Path(__file__).resolve().parents[2], temp, temp, audit)
        self.assertEqual(1, len(groups))
        self.assertEqual(302, len(groups[0]['evidence']))
        self.assertIn('Schema.Examples', groups[0]['change'])
        self.assertIn('model.go#L', groups[0]['location'])
        self.assertIn('unknown nested', groups[0]['test'])

    def test_distinct_unknown_fields_remain_distinct(self):
        audit = {'findings': [{'stage': 'parse', 'kind': 'failure', 'evidence': [{'path': 'mdm/commands/a.yaml',
                   'detail': 'line 2: field x not found in type schemagen.Schema\nline 3: field y not found in type schemagen.Schema'}]}]}
        with tempfile.TemporaryDirectory() as temp:
            groups = findings('generate', 'error', temp, temp, temp, audit)
        self.assertEqual(2, len(groups))
        self.assertNotEqual(groups[0]['key'], groups[1]['key'])

    def test_schema_diff_and_unsupported_type_guidance(self):
        with tempfile.TemporaryDirectory() as temp:
            before, after = Path(temp) / 'before', Path(temp) / 'after'
            for root, scalar in ((before, '<string>'), (after, '<future>')):
                (root / 'mdm/commands').mkdir(parents=True)
                (root / 'mdm/commands/a.yaml').write_text('type: ' + scalar + '\n')
            groups = findings('generate', 'schemagen: naming: mdm/commands/a.yaml: unsupported type "<future>" at Key', temp, before, after)
        self.assertIn('resolveType', groups[0]['change'])
        self.assertIn('-type: <string>', groups[0]['examples'][0]['diff'])
        self.assertIn('+type: <future>', groups[0]['examples'][0]['diff'])

    def test_unknown_errors_do_not_claim_a_verified_fix(self):
        item = diagnosis('compile', 'undefined: Unexpected')
        self.assertIn('needs investigation', item['why'])
        self.assertIn('has not verified', item['change'])
        item = diagnosis('parse', 'yaml: line 8: did not find expected key')
        self.assertIn('upstream correction', item['why'])

    def test_non_generator_audit_findings_are_ignored(self):
        audit = {'findings': [{'stage': 'audit', 'kind': 'review', 'evidence': []}]}
        with tempfile.TemporaryDirectory() as temp:
            groups = findings('generate', 'format: error', temp, temp, temp, audit)
        self.assertEqual(['generate'], [g['stage'] for g in groups])


class AssessmentTests(unittest.TestCase):
    def test_clean_lock_does_not_change_production_source(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            path = root / m.SCHEMA
            path.mkdir(parents=True)
            (path / 'EXPORTED_IDENTIFIERS.lock').write_text('OldType')
            (path / 'support.go').write_text('package support')
            m.prepare_output(root)
            self.assertFalse((path / 'EXPORTED_IDENTIFIERS.lock').exists())
            self.assertEqual('package support', (path / 'support.go').read_text())

    def test_real_go_compiler_rejects_invalid_output(self):
        # Generation can exit zero while producing syntactically valid Go with
        # invalid types. Exercise the actual compiler, not a mocked subprocess.
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / 'go.mod').write_text('module example.test/generated\n\ngo 1.27\n')
            (root / 'output.go').write_text('package generated\nvar GeneratedField MissingType\n')
            result = report()
            ok, text = m.stage(result, 'compile', ['go', 'build', './...'], root, root, dict(m.os.environ, GOWORK='off'))
            self.assertFalse(ok)
            self.assertIn('undefined: MissingType', text)
            (root / 'output.go').write_text('package generated\nvar GeneratedField string\n')
            ok, _ = m.stage(result, 'compile', ['go', 'build', './...'], root, root, dict(m.os.environ, GOWORK='off'))
            self.assertTrue(ok)

    def test_dependency_and_missing_checkout_are_automation_failures(self):
        self.assertTrue(m.infrastructure_failure('dial tcp: no such host'))
        self.assertTrue(m.infrastructure_failure('open /cache/go-build/data: operation not permitted'))
        self.assertFalse(m.infrastructure_failure('undefined: SchemaType'))
        with tempfile.TemporaryDirectory() as temp, patch.object(m, 'checkout', side_effect=OSError('checkout unavailable')):
            result = m.assess(Path(temp), manifest(report()), report()['branch'], Path(temp))
            self.assertFalse(result['complete'])
            self.assertEqual([], result['findings'])
            self.assertIn('checkout unavailable', result['automationErrors'])

    def test_artifact_provenance_must_match(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp)
            result = report()
            m.write_json(path / result['branch']['key'] / 'result.json', dict(result, projectCommit=NEXT))
            with self.assertRaises(ValueError):
                m.collect_reports(manifest(result), path)


class GeneratorIntegrationTests(unittest.TestCase):
    """Exercise the repository generator with actual valid and failing YAML."""
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory(prefix='dm-generator-test-')
        cls.addClassCleanup(cls.temp.cleanup)
        cls.tool = Path(cls.temp.name) / 'schemagen'
        root = Path(__file__).resolve().parents[2]
        cls.env = dict(m.os.environ, GOWORK='off')
        m.run(['go', 'build', '-o', cls.tool, './cmd/schemagen'], root, cls.env)

    def test_supported_addition_generates_and_unknown_field_is_diagnosed(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            source, output = root / 'apple', root / 'output'
            (source / 'mdm/commands').mkdir(parents=True)
            m.run(['git', 'init', '-qb', 'seed_future'], source)
            m.run(['git', 'config', 'user.name', 'Test'], source)
            m.run(['git', 'config', 'user.email', 'test@example.com'], source)
            yaml = source / 'mdm/commands/example.yaml'
            yaml.write_text('title: Example\npayload:\n  requesttype: Example\n  supportedOS: {}\npayloadkeys:\n'
                            '- key: FutureSupportedField\n  type: <string>\n  presence: optional\n')
            m.run(['git', 'add', '.'], source)
            m.run(['git', 'commit', '-qm', 'candidate'], source)
            base = [self.tool, '-schema', source, '-history', '', '-ref', 'seed_future', '-out', output]
            m.run(base + ['generate'], root, self.env)
            m.run(base + ['verify'], root, self.env)
            generated = '\n'.join(p.read_text() for p in output.rglob('*.gen.go'))
            self.assertIn('FutureSupportedField', generated)
            yaml.write_text(yaml.read_text() + 'future-metadata: true\n')
            with self.assertRaises(subprocess.CalledProcessError) as caught:
                m.run(base + ['generate'], root, self.env)
            self.assertIn('field future-metadata not found in type schemagen.Schema', caught.exception.stderr)
            result = findings('generate', caught.exception.stderr, Path(__file__).resolve().parents[2], source, source)
            self.assertEqual(1, len(result))
            self.assertEqual('codegen:unknown-field:schemagen.Schema:future-metadata', result[0]['key'])


class IncidentTests(unittest.TestCase):
    def test_same_cause_across_seeds_is_one_incident_and_rerun_is_silent(self):
        first, second = failure(report()), failure(report(commit='d' * 40, ref='seed_other'))
        proposed = actions([], first, second)
        self.assertEqual(1, len(proposed))
        issue = stored(proposed[0])
        self.assertEqual(2, len(m.metadata(issue['body'])['candidates']))
        self.assertEqual([], actions([issue], first, second))

    def test_control_failure_cannot_be_attributed_to_future_schema(self):
        base, future = failure(control()), failure(report())
        self.assertEqual([], m.issue_actions([], [base, future], manifest(future), 'run', 'owner/repo'))

    def test_no_changes_produce_no_incident(self):
        self.assertEqual([], actions([], report()))

    def test_all_affected_candidates_must_pass_before_closing(self):
        first, second = failure(report()), failure(report(commit='d' * 40))
        issue = stored(actions([], first, second)[0])
        self.assertEqual([], actions([issue], report()))
        proposed = actions([issue], report(), report(commit='d' * 40))
        self.assertEqual('closed', proposed[0][2]['state'])
        self.assertEqual('verified', m.metadata(proposed[0][2]['body'])['status'])

    def test_moved_failing_tip_preserves_unverified_previous_candidate(self):
        original = failure(report())
        issue = stored(actions([], original)[0])
        moved = failure(report(commit='d' * 40))
        updated = actions([issue], moved)[0]
        self.assertEqual({NEXT, 'd' * 40}, {b['commit'] for b in m.metadata(updated[2]['body'])['candidates']})
        self.assertEqual([], actions([stored(updated)], moved))

    def test_notes_survive_changes_and_recurrence_reopens(self):
        candidate = failure(report())
        issue = stored(actions([], candidate)[0])
        issue['body'] = 'Engineer preface\n' + issue['body'] + '\nEngineer conclusion'
        candidate['findings'][0]['evidence'].append({'path': 'b.yaml', 'detail': 'same issue'})
        body = actions([issue], candidate)[0][2]['body']
        self.assertTrue(body.startswith('Engineer preface'))
        self.assertTrue(body.endswith('Engineer conclusion'))
        closed = dict(issue, **actions([issue], report())[0][2])
        self.assertEqual('open', actions([closed], candidate)[0][2]['state'])

    def test_missing_artifact_prevents_closure(self):
        item = failure(report())
        issue = stored(actions([], item)[0])
        result = report()
        result['complete'] = False
        self.assertEqual([], actions([issue], result))

    def test_legacy_incidents_are_not_migrated_into_future_failures(self):
        old = {'number': 75, 'state': 'open', 'body': '<!-- schema-monitor {"key":"old","stage":"parse"} -->\n'
               'cannot unmarshal !!map into []schemagen.Example\nHuman notes'}
        old['body'] = old['body'].replace('\"stage\":\"parse\"', '\"stage\":\"parse\",\"candidate\":\"' + PIN + '\"')
        self.assertEqual([], actions([old], report()))
        second = dict(old, number=76)
        changes = m.consolidation_actions([second, old], PIN)
        self.assertEqual(2, len(changes))
        self.assertIn('#76', changes[0][2]['body'])
        self.assertIn('#75', changes[1][2]['body'])
        self.assertIn('Human notes', changes[0][2]['body'])
        self.assertIn('does not claim', changes[0][2]['body'])
        self.assertEqual('not_planned', changes[0][2]['state_reason'])
        revised = [dict(old, **changes[0][2]), dict(second, **changes[1][2])]
        self.assertEqual([], m.consolidation_actions(revised, PIN))


class PublicationTests(unittest.TestCase):
    def test_secondary_limit_respects_retry_after_and_does_not_retry_permissions(self):
        limited = subprocess.CompletedProcess([], 1, 'HTTP/2.0 403 Forbidden\nRetry-After: 2\n\n{}', 'secondary rate limit')
        good = subprocess.CompletedProcess([], 0, 'HTTP/2.0 201 Created\n\n{"number":1}', '')
        waits = []
        api = m.GitHub('owner/repo', sleep=waits.append)
        with patch.object(m.subprocess, 'run', side_effect=[limited, good]):
            self.assertEqual({'number': 1}, api.request('POST', 'issues', {}))
        self.assertIn(2.0, waits)
        denied = subprocess.CompletedProcess([], 1, 'HTTP/2.0 403 Forbidden\n\n{}', 'Resource not accessible')
        with patch.object(m.subprocess, 'run', return_value=denied) as run:
            with self.assertRaises(RuntimeError):
                api.request('POST', 'issues', {})
            self.assertEqual(1, run.call_count)

    def test_new_issue_cap_and_resume(self):
        future = failure(report())
        first = future['findings'][0]
        future['findings'] = [dict(copy.deepcopy(first), key=f'codegen:distinct:{i}') for i in range(12)]
        class API:
            repository = 'owner/repo'
            def __init__(self): self.existing = []; self.calls = []
            def issues(self): return self.existing
            def request(self, method, endpoint, body):
                self.calls.append((method, endpoint, body))
                self.existing.append(dict(body, state='open', number=len(self.existing) + 1))
        api = API()
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            for r in [control(), future]:
                m.write_json(root / r['branch']['key'] / 'result.json', r)
            self.assertFalse(m.publish(manifest(future), root, api, False, 'run'))
            self.assertEqual(10, len(api.calls))
            self.assertEqual(2, len(json.loads((root / 'publication.json').read_text())['deferred']))
            m.publish(manifest(future), root, api, False, 'run')
            self.assertEqual(12, len(api.calls))
            m.publish(manifest(future), root, api, False, 'different-run')
            self.assertEqual(12, len(api.calls))

    def test_report_only_never_mutates_and_pipeline_failure_remains_visible(self):
        from unittest.mock import Mock
        future = failure(report())
        api = Mock(repository='owner/repo')
        api.issues.return_value = []
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            for r in [control(), future]:
                m.write_json(root / r['branch']['key'] / 'result.json', r)
            self.assertFalse(m.publish(manifest(future), root, api, True, 'run'))
            api.request.assert_not_called()
            self.assertEqual(1, len(json.loads((root / 'proposed-issues.json').read_text())))

    def test_failed_write_retains_remaining_actions(self):
        from unittest.mock import Mock
        future = failure(report())
        api = Mock(repository='owner/repo')
        api.issues.return_value = []
        api.request.side_effect = RuntimeError('rate limited')
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            for r in [control(), future]:
                m.write_json(root / r['branch']['key'] / 'result.json', r)
            self.assertFalse(m.publish(manifest(future), root, api, False, 'run'))
            state = json.loads((root / 'publication.json').read_text())
            self.assertEqual([0], state['deferred'])
            self.assertEqual('rate limited', state['error'])


if __name__ == '__main__':
    unittest.main()
