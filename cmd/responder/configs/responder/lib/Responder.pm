use strict;
use warnings FATAL => 'all';

use Data::Dumper;
use Config::Simple;
use JSON;
use DBI;

our %Config;
our $IniPath;
our %IniCache;

package Responder;

our $dbh;

sub SetIniPath($) {
 ($IniPath)=@_;
 #print "Set inipath to $IniPath\n";
}

sub GetUser { return $ENV{'USER'}; }

sub s8f {
 my @Ret;
 foreach (@_)
  {
   $_=0 if(!defined($_));
   push @Ret, sprintf('%.08f', $_);
  }
 return @Ret;
}

sub s3f {
 my @Ret;
 foreach (@_)
  {
   $_=0 if(!defined($_));
   push @Ret, sprintf('%.03f', $_);
  }
 return @Ret;
}

sub b {
 my @Ret;
 foreach (@_)
  {
   $_='false' if(!defined($_));
   if($_ eq '0') { $_='false'; }
   elsif($_ eq '1') { $_='true'; }
   push @Ret, $_;
  }
 return @Ret;
}

sub s {
 my @Ret;
 foreach (@_)
  {
   $_='' if(!defined($_));
   push @Ret, $_;
  }
 return @Ret;
}

sub MakeDbConnect
 {
  my ($Which, $User)=@_;
  require DBI;
  return $dbh if($dbh);
  $User=$Which unless($User);
  print "Caw\n";
  my $dbh=DBI->connect('dbi:Pg:dbname='.$Which, $User, '', { PrintError=>1 });
  print "Caw\n";
  return $dbh;
 }

sub ConnectToDb ($$)
{
 my ($Config, $Prefix)=@_;
 my $dbhName=$$Config{$Prefix.'.dbname'} or die "Database name not present ($Prefix.dbname)!";
 my $dbhUser=$$Config{$Prefix.'.dbuser'} or die "Database user not present ($Prefix.dbuser')!";
 my $dbhHost=$$Config{$Prefix.'.dbhost'} or die "Database host not present ($Prefix.dbhost')!";
 my $dbtype =$$Config{$Prefix.".dbtype"} or die "Database type not specified ($Prefix.dbtype)!";
 my $dbhPassword=$$Config{$Prefix.'.dbpass'};
 my $dbh;
 if($dbtype eq 'mysql') {
  $dbh=DBI->connect("DBI:mysql:database=$dbhName;host=$dbhHost", $dbhUser, $dbhPassword);
 } elsif($dbtype eq 'pgsql') {
  $dbh=DBI->connect('dbi:Pg:dbname='.$dbhName, $dbhUser, '', { PrintError=>1 });
 } else {
  print "Fatal error: dbtype $dbtype undefined in $Prefix.dbtype\n";
 }
 return $dbh;
}

sub ExitIfPidActive
{
 my ($PidPath)=@_;
 if(-f $PidPath)
 {
  open FILE, $PidPath;
  my $Pid=<FILE>;
  close FILE;
  chomp $Pid;
  my $exists = kill 0, $Pid;
  if($exists)
  {
   print "PID $Pid is still running; exiting\n";
   exit;
  }
  print "PID $Pid is gone; grabbing PID and starting up.\n";
 }
 open FILE, ">$PidPath";
 print FILE $$;
 close FILE;
}

sub GetConfigOrDie($) {
 my ($Key)=@_;
 if(!exists($Config{$Key})) {
  printf "FAILED to get config key $Key!\n";
  exit(3);
 }
 return $Config{$Key};
}

sub ConnectToDbViaKey ($) {
 my ($Key)=@_;
 my $Section=GetConfigOrDie($Key);
 return ConnectToDb(\%Config, $Section);
}

sub GetFileMtime {
 my ($File)=@_;
 return (stat($File))[9];
}

sub ChatViaDb {
 my ($Config, $text)=@_;
 return if($text eq "");
 $text=~s/\n$//;
 unless($$Config{'channel'}) { print "ERROR: Null channel in chat config.\n"; return undef; }
 unless($$Config{'handle'}) { print "ERROR: Null handle (chat user) in chat config.\n"; return undef; }
 unless($$Config{'dbh'}) { print "ERROR: Null database handle in chat config.\n"; return undef; }
 if($$Config{'channel'}=~m/^stdout\-/)
 {
  printf "Special channel '%s' specified for chat; printing output instead of Slack output!\n%s\n", $$Config{'channel'}, $text;
  return 1;
 }
 my $dbh=$$Config{'dbh'};
 my $Query=sprintf("insert into chat_messages (handle,status,written,channel,message) values (%s, 'PENDING', CURRENT_TIME(), '%s', %s);", $dbh->quote($$Config{'handle'}), $$Config{'channel'}, $dbh->quote($text));
 $dbh->do($Query);
 if($dbh->errstr)
 {
  printf "ERROR: '%s'; failed to run '%s'.\n", $dbh->errstr, $Query;
  if($dbh->errstr)
  {
   print "...error persists after disconnect.\n";
   return undef;
  }
 }
 return $dbh->{'mysql_insertid'};
}

sub UpdateChatViaDb {
 my ($Config, $UpdateId, $text)=@_;
 return if($text eq "");
 $text=~s/\n$//;
 unless($$Config{'dbh'}) { print "ERROR: Null database handle in chat config.\n"; return undef; }
 if($$Config{'channel'}=~m/^stdout\-/)
 {
  printf "Special channel '%s' specified for chat; printing output instead of actually updating the stated message!\n%s\n", $$Config{'channel'}, $text;
  return 1;
 }
 my $dbh=$$Config{'dbh'};
 my $Query=sprintf("REPLACE INTO chat_updates (chatid,status,written,message) values (%d, 'PENDING', CURRENT_TIME(), %s);", $UpdateId, $dbh->quote($text));
 $dbh->do($Query);
 if($dbh->errstr)
 {
  printf "ERROR: '%s'; failed to run '%s'.\n", $dbh->errstr, $Query;
  if($dbh->errstr)
  {
   print "...error persists after disconnect.\n";
   return undef;
  }
 }
 return 1;
}

sub LoadAnIni($)
{
 my ($IniPath)=@_;
 my %Ini;
 if($IniCache{$IniPath})
 {
  return %{$IniCache{$IniPath}};
 }
 unless(-r $IniPath)
 {
  die "ERROR: Cannot access '$IniPath' to load config ini!\n";
 }
 Config::Simple->import_from($IniPath, \%Ini) or die "ERROR: Can't load $IniPath!";
 if($Ini{'secrets.fallback'})
 {
  my %Merge;
  Config::Simple->import_from($Ini{'secrets.fallback'}, \%Merge) or die "ERROR: Can't load '$IniPath': '$!'!";
  foreach (keys %Merge)
  {
   $Ini{$_}=$Merge{$_};
  }
 }
 %{$IniCache{$IniPath}}=%Config;
 return %Ini;
}

sub LoadConfigIni($)
{
 my ($Default)=@_;
 if(%Config)
 {
  # foreach (sort keys %Config) { print "$_\n"; }
  return %Config;
 }
 $IniPath=$Default;
 $IniPath=$ENV{'INIFILE'} if($ENV{'INIFILE'});
 %Config=LoadAnIni($IniPath);
 return %Config;
}

sub LoadJsonFile($) {
 my $Filename=shift;
 my $Buffer;
 open FILE, "<$Filename" or die "ERROR: JSON file '$Filename' we were told to load is inaccessible!\n";
 while(<FILE>) { $Buffer.=$_; }
 close FILE;
 return json_decode($Buffer);
}

sub json_decode {
 my $ret;
 my $json=shift;
 my $opt=shift;
 eval {
  $ret=JSON::decode_json($json);
  1;
 };
 if(defined($opt)) {
  if($opt eq 'debug')
   {
    if ( $@ ) { print "$@\n"; }
   }
 }
 return %$ret;
}

1;

