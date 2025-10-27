#!/bin/bash

# Start test environment script for wpgx testsuite
# This script helps you start PostgreSQL and Redis for testing

set -e

echo "Starting test environment..."

# Check if Docker is running
if ! docker info > /dev/null 2>&1; then
    echo "Error: Docker is not running. Please start Docker first."
    exit 1
fi

# Start services using Docker Compose
echo "Starting PostgreSQL and Redis containers..."
docker-compose -f docker-compose.test.yml up -d

# Wait for services to be ready
echo "Waiting for services to be ready..."
echo "PostgreSQL:"
docker-compose -f docker-compose.test.yml exec postgres pg_isready -U postgres

echo "Redis:"
docker-compose -f docker-compose.test.yml exec redis redis-cli ping

echo ""
echo "✅ Test environment is ready!"
echo ""
echo "You can now run your tests:"
echo "  POSTGRES_APPNAME=test go test ./..."
echo ""
echo "To stop the environment:"
echo "  docker-compose -f docker-compose.test.yml down"
echo ""
echo "To view logs:"
echo "  docker-compose -f docker-compose.test.yml logs -f"
