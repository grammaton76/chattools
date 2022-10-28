#!/usr/bin/perl
use strict;
use Data::Dumper;
print "Caw - this is the perl output\n";
printf("Arguments provided: %s\n", Dumper \@ARGV);
my $Env;
foreach (sort keys %ENV) {
  $Env.=sprintf("%s: %s\n", $_, $ENV{$_});
}
printf("Environment variables:\n%s\n", $Env);
