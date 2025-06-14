<?php

include_once __DIR__ . '/inc/config.php';

try {
    $dsn = "pgsql:host={$config['host']};port={$config['port']};dbname={$config['database']}";
    $pdo = new PDO($dsn, $config['user'], $config['password']);
    $pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
} catch (PDOException $e) {
    die("Database connection failed: " . $e->getMessage());
}

$from = $_GET['from'];
$to = $_GET['to'];
$host = isset($_GET['host']) ? ($_GET['host'] ?? '') : '';

$sql = 'SELECT "at", "host", "evt" FROM "public"."events" WHERE "at" BETWEEN :from AND :to';
if (!empty($host)) {
    $sql .= " AND host = :host";
}
$stmt = $pdo->prepare($sql);
$stmt->bindParam(':from', $from);
$stmt->bindParam(':to', $to);
if (!empty($host)) {
    $stmt->bindParam(':host', $host);
}
$stmt->execute();
$events = $stmt->fetchAll(PDO::FETCH_ASSOC);

header('Content-Type: application/json');

echo json_encode($events);
