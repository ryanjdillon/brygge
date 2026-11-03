{ ... }:
let
  # Every resource here exists only when var.ses_enabled is true.
  enabled = "\${var.ses_enabled ? 1 : 0}";
  regionalDkimRegions = builtins.concatStringsSep ", " (map (r: "\"${r}\"") [
    "af-south-1" "ap-south-2" "ap-southeast-3" "ap-southeast-5" "ca-west-1"
    "ap-northeast-3" "eu-south-1" "eu-central-2" "il-central-1"
    "me-central-1" "us-gov-east-1"
  ]);
in
{
  # Names are derived from club_slug alone so nix/host.nix can build the
  # same strings from clubConfig.slug without reading Terraform outputs.
  locals = {
    ses_name        = "brygge-\${var.club_slug}";
    ses_events_name = "brygge-\${var.club_slug}-ses-events";
    # Easy DKIM CNAME target. The provider does not export the identity's
    # SigningHostedZone, so it comes from the per-region table in
    # https://docs.aws.amazon.com/general/latest/gr/ses.html#ses_dkim_domains
    # The listed regions use dkim.<region>.amazonses.com; every other one
    # (eu-north-1 included) uses dkim.amazonses.com. Check the SES
    # console's DNS records after apply when changing aws_region.
    ses_dkim_zone = "\${contains([${regionalDkimRegions}], var.aws_region) ? \"dkim.\${var.aws_region}.amazonses.com\" : \"dkim.amazonses.com\"}";
  };

  data.aws_caller_identity.current = {
    count = enabled;
    lifecycle.precondition = [
      {
        condition     = "\${local.aws_keys_set}";
        error_message = "ses_enabled is true but aws_access_key / aws_secret_key are empty in terraform.tfvars.json.";
      }
      {
        condition     = "\${var.club_slug != \"\"}";
        error_message = "ses_enabled is true but club_slug is empty; it names the SES and SQS resources.";
      }
    ];
  };

  # Domain identity with Easy DKIM. SES publishes three selector tokens;
  # dns.nix turns them into `<token>._domainkey` CNAMEs. Signing with
  # d=<domain> is what makes DMARC align for mail sent through SES.
  # https://docs.aws.amazon.com/ses/latest/dg/send-email-authentication-dkim-easy.html
  resource.aws_sesv2_email_identity.domain = {
    count                  = enabled;
    email_identity         = "\${var.domain}";
    configuration_set_name = "\${aws_sesv2_configuration_set.main[0].configuration_set_name}";
    dkim_signing_attributes.next_signing_key_length = "RSA_2048_BIT";
  };

  # Custom MAIL FROM so the envelope sender (and therefore SPF) is on our
  # domain and aligns under relaxed DMARC. `send.` because `bounce.` is
  # already Stripe's. USE_DEFAULT_VALUE keeps mail flowing (unaligned SPF,
  # DKIM still aligns) if the MX record is ever missing.
  # https://docs.aws.amazon.com/ses/latest/dg/mail-from.html
  resource.aws_sesv2_email_identity_mail_from_attributes.domain = {
    count                  = enabled;
    email_identity         = "\${aws_sesv2_email_identity.domain[0].email_identity}";
    mail_from_domain       = "send.\${var.domain}";
    behavior_on_mx_failure = "USE_DEFAULT_VALUE";
  };

  # Open/click tracking is deliberately absent: it rewrites links, which
  # would break magic links and payment links.
  resource.aws_sesv2_configuration_set.main = {
    count                  = enabled;
    configuration_set_name = "\${local.ses_name}";
    reputation_options.reputation_metrics_enabled = true;
    sending_options.sending_enabled               = true;
    suppression_options.suppressed_reasons        = [ "BOUNCE" "COMPLAINT" ];
  };

  # Every delivery outcome Brygge records, published to SNS.
  # https://docs.aws.amazon.com/ses/latest/dg/event-publishing-add-event-destination-sns.html
  resource.aws_sesv2_configuration_set_event_destination.sns = {
    count                  = enabled;
    # Publishing is allowed by the topic policy; create it first so the
    # first events after apply are not refused.
    depends_on             = [ "aws_sns_topic_policy.ses_events" ];
    configuration_set_name = "\${aws_sesv2_configuration_set.main[0].configuration_set_name}";
    event_destination_name = "sns";
    event_destination = {
      enabled              = true;
      matching_event_types = [
        "SEND"
        "DELIVERY"
        "DELIVERY_DELAY"
        "BOUNCE"
        "COMPLAINT"
        "REJECT"
        "RENDERING_FAILURE"
      ];
      sns_destination.topic_arn = "\${aws_sns_topic.ses_events[0].arn}";
    };
  };

  resource.aws_sns_topic.ses_events = {
    count = enabled;
    name  = "\${local.ses_events_name}";
  };

  # SES may publish only on behalf of this account's configuration set,
  # which closes the confused-deputy path for other accounts.
  data.aws_iam_policy_document.ses_events_topic = {
    count = enabled;
    statement = [{
      sid       = "AllowSESPublish";
      actions   = [ "sns:Publish" ];
      resources = [ "\${aws_sns_topic.ses_events[0].arn}" ];
      principals = [{ type = "Service"; identifiers = [ "ses.amazonaws.com" ]; }];
      condition = [
        {
          test     = "StringEquals";
          variable = "aws:SourceAccount";
          values   = [ "\${data.aws_caller_identity.current[0].account_id}" ];
        }
        {
          test     = "ArnLike";
          variable = "aws:SourceArn";
          values   = [ "\${aws_sesv2_configuration_set.main[0].arn}" ];
        }
      ];
    }];
  };

  resource.aws_sns_topic_policy.ses_events = {
    count  = enabled;
    arn    = "\${aws_sns_topic.ses_events[0].arn}";
    policy = "\${data.aws_iam_policy_document.ses_events_topic[0].json}";
  };

  # SQS between SNS and Brygge means no public webhook endpoint and no
  # lost events while Brygge is down: the worker long-polls and deletes
  # only after storing. Messages that keep failing land in the DLQ, kept
  # for the maximum 14 days so they can be inspected and redriven.
  resource.aws_sqs_queue.ses_events_dlq = {
    count                     = enabled;
    name                      = "\${local.ses_events_name}-dlq";
    message_retention_seconds = 1209600;
    sqs_managed_sse_enabled   = true;
  };

  resource.aws_sqs_queue.ses_events = {
    count                      = enabled;
    name                       = "\${local.ses_events_name}";
    message_retention_seconds  = 345600;
    visibility_timeout_seconds = 60;
    receive_wait_time_seconds  = 20;
    sqs_managed_sse_enabled    = true;
    redrive_policy = "\${jsonencode({ deadLetterTargetArn = aws_sqs_queue.ses_events_dlq[0].arn, maxReceiveCount = 5 })}";
  };

  # https://docs.aws.amazon.com/sns/latest/dg/sns-sqs-as-subscriber.html
  data.aws_iam_policy_document.ses_events_queue = {
    count = enabled;
    statement = [{
      sid        = "AllowSESEventsTopic";
      actions    = [ "sqs:SendMessage" ];
      resources  = [ "\${aws_sqs_queue.ses_events[0].arn}" ];
      principals = [{ type = "Service"; identifiers = [ "sns.amazonaws.com" ]; }];
      condition = [{
        test     = "ArnEquals";
        variable = "aws:SourceArn";
        values   = [ "\${aws_sns_topic.ses_events[0].arn}" ];
      }];
    }];
  };

  resource.aws_sqs_queue_policy.ses_events = {
    count     = enabled;
    queue_url = "\${aws_sqs_queue.ses_events[0].id}";
    policy    = "\${data.aws_iam_policy_document.ses_events_queue[0].json}";
  };

  # Raw delivery makes the SQS body the SES event JSON itself, without
  # the SNS envelope the worker would otherwise have to unwrap.
  resource.aws_sns_topic_subscription.ses_events = {
    count                = enabled;
    topic_arn            = "\${aws_sns_topic.ses_events[0].arn}";
    protocol             = "sqs";
    endpoint             = "\${aws_sqs_queue.ses_events[0].arn}";
    raw_message_delivery = true;
  };

  # Brygge's runtime principal. Its access key is created by hand in the
  # IAM console and goes straight to the host's secret env file: an
  # aws_iam_access_key resource would store the secret in plaintext in
  # the remote state.
  resource.aws_iam_user.app = {
    count = enabled;
    name  = "\${local.ses_name}-app";
  };

  data.aws_iam_policy_document.app = {
    count = enabled;
    statement = [
      {
        # SES SMTP authorises against SendRawEmail on both the sending
        # identity and the configuration set named in the message.
        sid       = "SendViaClubIdentity";
        actions   = [ "ses:SendRawEmail" ];
        resources = [
          "\${aws_sesv2_email_identity.domain[0].arn}"
          "\${aws_sesv2_configuration_set.main[0].arn}"
        ];
      }
      {
        sid     = "ConsumeDeliveryEvents";
        actions = [
          "sqs:ReceiveMessage"
          "sqs:DeleteMessage"
          "sqs:GetQueueUrl"
        ];
        resources = [ "\${aws_sqs_queue.ses_events[0].arn}" ];
      }
      {
        # The worker only reads the DLQ's depth; inspecting and
        # redriving its messages is an operator task.
        sid       = "WatchDeadLetterQueue";
        actions   = [ "sqs:GetQueueUrl" "sqs:GetQueueAttributes" ];
        resources = [ "\${aws_sqs_queue.ses_events_dlq[0].arn}" ];
      }
      {
        # The account-level suppression list has no resource ARN, so
        # these two actions cannot be scoped further.
        sid       = "ManageSuppression";
        actions   = [ "ses:GetSuppressedDestination" "ses:DeleteSuppressedDestination" ];
        resources = [ "*" ];
      }
    ];
  };

  resource.aws_iam_user_policy.app = {
    count  = enabled;
    name   = "\${local.ses_name}-app";
    user   = "\${aws_iam_user.app[0].name}";
    policy = "\${data.aws_iam_policy_document.app[0].json}";
  };
}
