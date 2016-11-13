provider "aws" {
  region = "us-east-1"
}

resource "random_id" "dev_bucket_prefix" {
  byte_length = 8
}

resource "aws_s3_bucket" "test" {
  bucket = "nlz-${random_id.dev_bucket_prefix.hex}-test-bucket"
  force_destroy = true
}

output "bucket" {
  value = "${aws_s3_bucket.test.bucket}"
}
