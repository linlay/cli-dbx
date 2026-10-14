import importlib.util
import pathlib
import tempfile
import unittest

ROOT=pathlib.Path(__file__).resolve().parents[2]
spec=importlib.util.spec_from_file_location('verify_shared',ROOT/'scripts/verify-shared-host.py')
verify_shared=importlib.util.module_from_spec(spec);spec.loader.exec_module(verify_shared)

class CanonicalHostTests(unittest.TestCase):
    def test_same_toolchain_different_host_rejected_and_original_preserved(self):
        with tempfile.TemporaryDirectory(dir=ROOT/'dist') as directory:
            host=pathlib.Path(directory)/'dbx'
            host.write_bytes(b'same toolchain but different package ABI')
            with self.assertRaisesRegex(ValueError,'differs from canonical'):
                verify_shared.verify(host,lambda output:output.write_bytes(b'canonical ABI'))
            self.assertEqual(host.read_bytes(),b'same toolchain but different package ABI')
    def test_matching_host_and_host_mutation(self):
        with tempfile.TemporaryDirectory(dir=ROOT/'dist') as directory:
            host=pathlib.Path(directory)/'dbx'
            host.write_bytes(b'canonical ABI')
            self.assertEqual(len(verify_shared.verify(host,lambda output:output.write_bytes(host.read_bytes()))),64)
            def mutation(output):
                output.write_bytes(b'canonical ABI')
                host.write_bytes(b'changed after inspection')
            with self.assertRaisesRegex(ValueError,'changed during'):
                verify_shared.verify(host,mutation)
