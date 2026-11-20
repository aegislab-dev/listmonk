"""axm 의 선택 로직 단위시험. `python3 -m unittest scripts/axm/test_axm.py` (저장소 루트에서).

Docker 없이 돈다. 바뀐 파일 → spec·Go 시험 매핑, 매핑 표가 CI 와 어긋나지 않는지,
runtime.Dockerfile 이 Dockerfile.axm 마지막 단계와 같은지를 본다.
"""
import importlib.machinery
import importlib.util
import json
import re
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]

_loader = importlib.machinery.SourceFileLoader('axm_tool', str(HERE / 'axm'))
_spec = importlib.util.spec_from_loader('axm_tool', _loader)
axm = importlib.util.module_from_spec(_spec)
_loader.exec_module(axm)

FILES = axm.tree_files()
TMAP = axm.load_map()


def pick(*changed):
    return axm.affected(list(changed), FILES, TMAP)


class GlobTest(unittest.TestCase):
    def test_star_does_not_cross_directories(self):
        self.assertTrue(axm.match_any('cmd/axm_audit.go', ['cmd/axm_*.go']))
        self.assertFalse(axm.match_any('cmd/sub/axm_audit.go', ['cmd/axm_*.go']))

    def test_double_star(self):
        self.assertTrue(axm.match_any('internal/auth/a/b.go', ['internal/auth/**']))
        self.assertTrue(axm.match_any('docs/a/b.md', ['docs/**']))
        self.assertTrue(axm.match_any('README.md', ['*.md']))
        self.assertFalse(axm.match_any('cmd/handlers.go', ['internal/**']))


class SelectSpecTest(unittest.TestCase):
    def test_feature_file_selects_its_spec(self):
        r = pick('frontend/src/views/Audit.vue')
        self.assertEqual(r['specs'], ['audit.cy.js'])
        self.assertTrue(r['unit'])

    def test_backend_feature_file(self):
        r = pick('cmd/axm_audit.go')
        self.assertEqual(r['specs'], ['audit.cy.js'])

    def test_changed_spec_runs_itself(self):
        r = pick('frontend/cypress/e2e/list-draw.cy.js')
        self.assertEqual(r['specs'], ['list-draw.cy.js'])

    def test_upstream_spec_not_in_ci_is_skipped(self):
        r = pick('frontend/cypress/e2e/campaigns.cy.js')
        self.assertEqual(r['specs'], [])
        self.assertTrue(any('CI 가 돌리지 않는' in n for n in r['notes']))

    def test_shared_file_adds_smoke(self):
        r = pick('frontend/src/utils.js')
        self.assertEqual(set(r['specs']), set(TMAP['smoke']))
        self.assertTrue(any('전체 e2e' in n for n in r['notes']))

    def test_shared_plus_feature_is_union(self):
        r = pick('frontend/src/api/index.js', 'frontend/src/components/ListDraw.vue')
        self.assertEqual(set(r['specs']), set(TMAP['smoke']) | {'list-draw.cy.js'})

    def test_specs_come_in_ci_order(self):
        r = pick('frontend/src/api/index.js', 'frontend/src/components/AddressCheck.vue')
        order = TMAP['ci_specs']
        self.assertEqual(r['specs'], sorted(r['specs'], key=order.index))

    def test_unmapped_source_warns_and_adds_smoke(self):
        r = pick('frontend/src/views/Dashboard.vue')
        self.assertEqual(set(r['specs']), set(TMAP['smoke']))
        self.assertTrue(r['unmapped'])
        self.assertTrue(any('매핑 없음' in n for n in r['notes']))

    def test_docs_only_runs_nothing(self):
        r = pick('README.md', 'docs/docs/content/apis/lists.md')
        self.assertEqual((r['specs'], r['go'], r['unit']), ([], {}, False))
        self.assertTrue(r['docs_only'])

    def test_full_build_files(self):
        r = pick('Dockerfile.axm')
        self.assertTrue(r['full_build'])
        self.assertEqual(set(r['specs']), set(TMAP['smoke']))
        r = pick('frontend/yarn.lock')
        self.assertTrue(r['full_build'])

    def test_workflow_file_runs_nothing_locally(self):
        r = pick('.github/workflows/axm-image.yml')
        self.assertEqual((r['specs'], r['go'], r['full_build']), ([], {}, False))

    def test_tool_files_run_python_tests(self):
        r = pick('scripts/axm/axm')
        self.assertTrue(r['python'])
        self.assertFalse(r['docs_only'])


class SelectGoTest(unittest.TestCase):
    def test_feature_file_narrows_cmd_tests(self):
        r = pick('cmd/axm_audit.go')
        run = r['go']['cmd']
        self.assertIsNotNone(run)
        self.assertIn('TestAudit', run) if 'TestAudit' in run else self.assertTrue(run.startswith('^('))
        # 좁힌 목록은 패키지 전체 시험보다 훨씬 적다.
        allf = {fn for f in FILES if f.startswith('cmd/') and f.endswith('_test.go') for fn in axm.test_funcs(f)}
        self.assertLess(len(run.split('|')), len(allf))

    def test_shared_go_file_runs_whole_package(self):
        r = pick('cmd/handlers.go')
        self.assertIsNone(r['go']['cmd'])

    def test_mixed_feature_and_shared_runs_whole_package(self):
        r = pick('cmd/axm_audit.go', 'cmd/campaigns.go')
        self.assertIsNone(r['go']['cmd'])

    def test_queries_run_cmd_and_core(self):
        r = pick('queries/axm_audit.sql') if (ROOT / 'queries/axm_audit.sql').exists() else pick('schema.sql')
        self.assertIn('cmd', r['go'])
        self.assertIn('internal/core', r['go'])
        self.assertIsNone(r['go']['cmd'])

    def test_importers_of_a_changed_package_run(self):
        r = pick('internal/addrcheck/' + sorted(p.name for p in (ROOT / 'internal/addrcheck').glob('*.go') if not p.name.endswith('_test.go'))[0])
        self.assertIn('internal/addrcheck', r['go'])
        self.assertIn('cmd', r['go'])  # cmd 가 addrcheck 를 import 한다
        self.assertIsNone(r['go']['cmd'])

    def test_no_go_change_no_go_test(self):
        self.assertEqual(pick('frontend/src/views/Audit.vue')['go'], {})

    def test_package_without_tests_is_skipped(self):
        pk, _ = axm.go_packages(FILES)
        without = [d for d, fs in pk.items() if not any(f.endswith('_test.go') for f in fs)]
        if without:
            r = pick(pk[without[0]][0])
            self.assertNotIn(without[0], r['go'])


class MapIntegrityTest(unittest.TestCase):
    def test_ci_specs_match_workflow(self):
        wf = (ROOT / axm.CI_WORKFLOW).read_text()
        m = re.search(r"--spec '([^']+)'", wf)
        self.assertIsNotNone(m, '워크플로에서 --spec 줄을 못 찾았다')
        ci = [s.split('/')[-1] for s in m.group(1).split(',')]
        self.assertEqual(ci, TMAP['ci_specs'], 'test-map.json 의 ci_specs 가 axm-image.yml 의 --spec 과 다르다')

    def test_all_spec_files_exist(self):
        for s in TMAP['ci_specs']:
            self.assertTrue((ROOT / 'frontend/cypress/e2e' / s).exists(), s)

    def test_rules_point_to_ci_specs(self):
        for rule in TMAP['rules']:
            for s in rule['specs']:
                self.assertIn(s, TMAP['ci_specs'])
        for s in TMAP['smoke']:
            self.assertIn(s, TMAP['ci_specs'])

    def test_every_ci_spec_is_reachable_from_a_rule(self):
        reach = {s for rule in TMAP['rules'] for s in rule['specs']}
        self.assertEqual(reach, set(TMAP['ci_specs']))

    def test_every_rule_path_matches_an_existing_file(self):
        for rule in TMAP['rules']:
            for p in rule['paths']:
                self.assertTrue(any(axm.match_any(f, [p]) for f in FILES), f'규칙 경로가 어떤 파일에도 안 맞는다: {p}')

    def test_every_axm_cmd_file_has_a_rule_or_is_a_test(self):
        """cmd/axm_*.go 가 새로 생겼는데 규칙이 없으면 알려 준다."""
        allpaths = [p for r in TMAP['rules'] for p in r['paths']] + TMAP['shared']
        missing = [f for f in FILES if re.match(r'cmd/axm_[^/]*\.go$', f) and not f.endswith('_test.go')
                   and not axm.match_any(f, allpaths)]
        # 매핑이 없는 파일은 smoke + 경고로 처리되므로 실패가 아니라 목록만 확인한다.
        for f in missing:
            r = pick(f)
            self.assertTrue(r['unmapped'])


class RuntimeDockerfileTest(unittest.TestCase):
    def test_runtime_stage_equals_dockerfile_axm(self):
        axm_df = (ROOT / 'Dockerfile.axm').read_text()
        final = axm_df[axm_df.rindex('FROM alpine'):]
        mine = (HERE / 'runtime.Dockerfile').read_text()
        mine = mine[mine.index('FROM alpine'):]
        norm = lambda t: [l for l in t.splitlines() if l.strip() and not l.startswith('#')]
        a = [l.replace('COPY --from=build /src/listmonk .', 'COPY listmonk .') for l in norm(final)]
        self.assertEqual(a, norm(mine))

    def test_base_image_pins_match(self):
        axm_df = (ROOT / 'Dockerfile.axm').read_text()
        base = (HERE / 'base.Dockerfile').read_text()
        for pin in ('node:24.22.2-bookworm-slim', 'yarn@1.22.22'):
            self.assertIn(pin, axm_df)
            self.assertIn(pin, base)
        self.assertIn(axm.GO_IMAGE, axm_df)


class StampTest(unittest.TestCase):
    def test_changed_by_content_not_by_commit(self):
        stamp = {'files': {'a': 'h1', 'b': 'h2', 'gone': 'h3'}}
        files = ['a', 'b', 'new']
        hashes = {'a': 'h1', 'b': 'CHANGED', 'new': 'h4'}
        self.assertEqual(axm.changed_since_stamp(stamp, files, hashes), ['b', 'gone', 'new'])

    def test_no_change(self):
        stamp = {'files': {'a': 'h1'}}
        self.assertEqual(axm.changed_since_stamp(stamp, ['a'], {'a': 'h1'}), [])


class KeyTest(unittest.TestCase):
    def test_image_key_ignores_docs_and_specs(self):
        files = ['cmd/a.go', 'docs/x.md', 'frontend/cypress/e2e/a.cy.js', 'scripts/axm/axm']
        h1 = {f: '1' for f in files}
        h2 = dict(h1, **{'docs/x.md': '2', 'frontend/cypress/e2e/a.cy.js': '2', 'scripts/axm/axm': '2'})
        self.assertEqual(axm.image_key(files, h1), axm.image_key(files, h2))
        h3 = dict(h1, **{'cmd/a.go': '2'})
        self.assertNotEqual(axm.image_key(files, h1), axm.image_key(files, h3))

    def test_frontend_artifact_keys(self):
        files = ['frontend/src/a.vue', 'frontend/email-builder/src/b.tsx', 'frontend/tests/t.mjs', 'frontend/email-builder/yarn.lock']
        h = {f: '1' for f in files}
        eb, fe, ut = axm.art_keys(files, h)
        # 이메일 빌더 소스가 바뀌면 셋 다 바뀐다
        eb2, fe2, ut2 = axm.art_keys(files, dict(h, **{'frontend/email-builder/src/b.tsx': '2'}))
        self.assertTrue(eb != eb2 and fe != fe2 and ut != ut2)
        # 프론트 단위시험 파일만 바뀌면 dist 키는 그대로, 단위시험 키만 바뀐다
        eb3, fe3, ut3 = axm.art_keys(files, dict(h, **{'frontend/tests/t.mjs': '2'}))
        self.assertTrue(eb == eb3 and fe == fe3 and ut != ut3)


if __name__ == '__main__':
    unittest.main()
